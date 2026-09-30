package main

// 更新中心：registry digest 对比检查 / 重建式容器更新 / 自动更新调度 / 防并发任务管理
import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------- 任务管理器（防并发） ----------

type TaskManager struct {
	mu      sync.Mutex
	running map[string]bool
}

func NewTaskManager() *TaskManager { return &TaskManager{running: map[string]bool{}} }

// Acquire 抢占任务锁，返回是否抢到（已被占用返回 false）。
// 手动更新走 Start（异步）；调度器走 Acquire/Release（同步串行），
// 两者共用同一把锁，因此自动更新与手动更新不会同时作用于同一容器。
func (tm *TaskManager) Acquire(key string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.running[key] {
		return false
	}
	tm.running[key] = true
	return true
}

func (tm *TaskManager) Release(key string) {
	tm.mu.Lock()
	delete(tm.running, key)
	tm.mu.Unlock()
}

func (tm *TaskManager) Running(key string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.running[key]
}

func (tm *TaskManager) Start(key string, fn func(progress func(msg string, status string, pct int))) bool {
	if !tm.Acquire(key) {
		return false
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				app.Logs.Add("ERROR", fmt.Sprintf("任务异常: %v", r), "realtime")
			}
			tm.Release(key)
		}()
		fn(updateProgress(key))
	}()
	return true
}

// updateProgress 生成向 WebSocket 广播进度的回调（前端顶部进度条消费）。
func updateProgress(key string) func(msg string, status string, pct int) {
	return func(msg string, status string, pct int) {
		app.Hub.Broadcast("update_progress", map[string]any{
			"container": strings.TrimPrefix(key, "update:"),
			"message":   msg, "status": status, "percentage": pct,
		})
	}
}

// ---------- 更新检查 ----------

type updateResult struct {
	ContainerID   string `json:"container_id"`
	ContainerName string `json:"container_name"`
	Image         string `json:"image"`
	LocalDigest   string `json:"local_digest"`
	RemoteDigest  string `json:"remote_digest"`
	HasUpdate     bool   `json:"has_update"`
	Status        string `json:"status"` // updatable | up-to-date | unknown
	CheckMethod   string `json:"check_method"`
}

// parseAuthChallenge 解析 registry 的 WWW-Authenticate 挑战头，提取 Bearer 认证三要素。
//
// 形如：Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:foo/bar:pull"
// realm 是取 token 的地址，各 registry 各不相同（Docker Hub 是 auth.docker.io，
// GHCR 是 ghcr.io/token，Quay 是 quay.io/v2/auth），因此绝不能硬编码。
// 值内部可能含逗号（scope 不会，但 realm 的 query 有可能），故按引号状态切分。
func parseAuthChallenge(h string) (realm, service, scope string) {
	h = strings.TrimSpace(h)
	const prefix = "bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", "", ""
	}
	rest := h[len(prefix):]
	var parts []string
	var cur strings.Builder
	inQuote := false
	for _, r := range rest {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ',' && !inQuote:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	for _, p := range parts {
		k, v, found := strings.Cut(p, "=")
		if !found {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch k {
		case "realm":
			realm = v
		case "service":
			service = v
		case "scope":
			scope = v
		}
	}
	return realm, service, scope
}

// fetchRegistryToken 向挑战头给出的 realm 换取匿名 pull token。
// 响应同时兼容 token / access_token 两种字段名（Docker Hub 用 token，部分 registry 用 access_token）。
func fetchRegistryToken(client *http.Client, realm, service, scope string) (string, error) {
	if realm == "" {
		return "", fmt.Errorf("registry 未提供认证 realm")
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if service != "" {
		q.Set("service", service)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()
	resp, err := client.Get(u.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("token 接口返回 HTTP %d", resp.StatusCode)
	}
	var tj struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tj); err != nil {
		return "", err
	}
	if tj.Token != "" {
		return tj.Token, nil
	}
	if tj.AccessToken != "" {
		return tj.AccessToken, nil
	}
	return "", fmt.Errorf("token 响应中无 token 字段")
}

// registryDigest 查询远程 manifest digest（任何匿名可读 registry 通用）。
//
// 采用标准的「挑战—应答」流程：先匿名请求 manifest，若被 401 拒绝，
// 就从 WWW-Authenticate 头里读出该 registry 自己的 realm 去换 token，再重试一次。
// 这样 Docker Hub / GHCR / Quay / 私有 registry 全都适用，无需为每个 host 写分支。
func registryDigest(image string) (string, error) {
	repo, tag := parseImage(image)
	host := "registry-1.docker.io"
	scopeRepo := repo
	if i := strings.Index(repo, "/"); i > 0 && strings.Contains(repo[:i], ".") {
		host = repo[:i]
		scopeRepo = repo[i+1:]
	} else if !strings.Contains(repo, "/") {
		scopeRepo = "library/" + repo
	}
	// docker.io 是 index.docker.io 的别名，真正的 API 端点是 registry-1.docker.io
	if host == "docker.io" || host == "index.docker.io" {
		host = "registry-1.docker.io"
	}
	client := &http.Client{Timeout: 20 * time.Second}
	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, scopeRepo, tag)
	const accept = "application/vnd.docker.distribution.manifest.v2+json," +
		"application/vnd.docker.distribution.manifest.list.v2+json," +
		"application/vnd.oci.image.index.v1+json," +
		"application/vnd.oci.image.manifest.v1+json"

	doReq := func(token string) (*http.Response, error) {
		req, err := http.NewRequest("GET", manifestURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", accept)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return client.Do(req)
	}

	resp, err := doReq("")
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		realm, service, scope := parseAuthChallenge(challenge)
		if scope == "" {
			scope = "repository:" + scopeRepo + ":pull"
		}
		token, terr := fetchRegistryToken(client, realm, service, scope)
		if terr != nil {
			return "", fmt.Errorf("获取 %s 的 registry token 失败: %w", host, terr)
		}
		resp, err = doReq(token)
		if err != nil {
			return "", err
		}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("registry 返回 HTTP %d", resp.StatusCode)
	}
	dg := resp.Header.Get("Docker-Content-Digest")
	if dg == "" {
		return "", fmt.Errorf("no digest header")
	}
	return dg, nil
}

// handleCheckUpdates POST /api/updates/check {container_name?}
func (a *App) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	specific := bodyStr(readBody(r), "container_name")
	results, err := a.checkUpdates(specific)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	n := 0
	for _, r := range results {
		if r.HasUpdate {
			n++
		}
	}
	a.Logs.Add("SUCCESS", fmt.Sprintf("更新检查完成: %d 个容器，%d 个可更新", len(results), n), "realtime")
	writeJSON(w, 200, map[string]any{"success": true, "data": results})
}

// checkUpdates 对比本地与远端 registry digest，返回每个容器的更新状态。
// specific 非空时只检查指定容器。供 HTTP 接口与 Telegram 机器人共用。
func (a *App) checkUpdates(specific string) ([]updateResult, error) {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return nil, err
	}
	imgs, _ := a.Docker.ListImages()
	localDigest := map[string]string{}
	for _, im := range imgs {
		for _, rd := range im.RepoDigests {
			parts := strings.SplitN(rd, "@", 2)
			if len(parts) == 2 {
				localDigest[parts[0]] = parts[1]
			}
		}
	}
	results := []updateResult{}
	for _, c := range cs {
		name := containerName(c)
		if specific != "" && name != specific {
			continue
		}
		repo, _ := parseImage(c.Image)
		res := updateResult{
			ContainerID: c.ID[:12], ContainerName: name, Image: c.Image,
			Status: "unknown", CheckMethod: "online",
		}
		res.LocalDigest = localDigest[repo]
		if dg, err := registryDigest(c.Image); err != nil {
			res.Status = "unknown"
		} else {
			res.RemoteDigest = dg
			if res.LocalDigest != "" {
				if strings.HasSuffix(res.LocalDigest, strings.TrimPrefix(dg, "sha256:")) ||
					strings.HasSuffix(dg, strings.TrimPrefix(res.LocalDigest, "sha256:")) {
					res.Status = "up-to-date"
				} else {
					res.Status = "updatable"
					res.HasUpdate = true
				}
			}
		}
		results = append(results, res)
		a.Store.mu.Lock()
		a.Store.Data.HasUpdate[name] = res.HasUpdate
		a.Store.Data.UpdateStatus[name] = res.Status
		a.Store.mu.Unlock()
	}
	a.Store.mu.Lock()
	a.Store.Data.LastCheck = a.Store.Now()
	// 结果整体落盘：这样刷新页面 / 重进「更新中心」时表格能直接回显，
	// 不用为了看到内容再跑一次 5-30 秒的 digest 检查。
	a.Store.Data.LastResults = results
	a.Store.mu.Unlock()
	a.Store.Save()
	return results, nil
}

// updateInterval 计算自动更新的实际间隔。0 天 0 小时 = 每周（与界面提示一致）。
func (a *App) updateInterval() time.Duration {
	a.Store.mu.RLock()
	d, h := a.Store.Data.UpdateIntervalDays, a.Store.Data.UpdateIntervalHours
	a.Store.mu.RUnlock()
	iv := time.Duration(d*24+h) * time.Hour
	if iv < time.Hour {
		iv = 7 * 24 * time.Hour
	}
	return iv
}

func (a *App) pruneInterval() time.Duration {
	a.Store.mu.RLock()
	h := a.Store.Data.AutoPruneIntervalHours
	a.Store.mu.RUnlock()
	iv := time.Duration(h) * time.Hour
	if iv < time.Hour {
		iv = 24 * time.Hour
	}
	return iv
}

// autoTargets 返回已「收藏」的自动更新容器名（排序后，保证执行顺序稳定）。
func (a *App) autoTargets() []string {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	out := []string{}
	for name, en := range a.Store.Data.AutoUpdate {
		if en {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// handleUpdateSettings GET/PUT /api/updates/settings
func (a *App) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.Store.mu.RLock()
		autoList := []string{}
		for name, en := range a.Store.Data.AutoUpdate {
			if en {
				autoList = append(autoList, name)
			}
		}
		sort.Strings(autoList)
		d := map[string]any{
			"update_interval_days":      a.Store.Data.UpdateIntervalDays,
			"update_interval_hours":     a.Store.Data.UpdateIntervalHours,
			"auto_update_containers":    autoList,
			"auto_update_last_run":      a.Store.Data.AutoUpdateLastRun,
			"auto_update_next_run":      a.Store.Data.AutoUpdateNextRun,
			"last_check":                a.Store.Data.LastCheck,
			"last_results":              a.Store.Data.LastResults,
			"auto_prune_enabled":        a.Store.Data.AutoPruneEnabled,
			"auto_prune_all":            a.Store.Data.AutoPruneAll,
			"auto_prune_interval_hours": a.Store.Data.AutoPruneIntervalHours,
			"auto_prune_last_run":       a.Store.Data.AutoPruneLastRun,
			"auto_prune_next_run":       a.Store.Data.AutoPruneNextRun,
			"auto_prune_last_freed":     a.Store.Data.AutoPruneLastFreed,
		}
		a.Store.mu.RUnlock()
		writeJSON(w, 200, map[string]any{"success": true, "data": d})
		return
	}

	m := readBody(r)
	a.Store.mu.Lock()
	intervalChanged := false
	if v, ok := m["update_interval_days"].(float64); ok {
		if int(v) != a.Store.Data.UpdateIntervalDays {
			a.Store.Data.UpdateIntervalDays = int(v)
			intervalChanged = true
		}
	}
	if v, ok := m["update_interval_hours"].(float64); ok {
		if int(v) != a.Store.Data.UpdateIntervalHours {
			a.Store.Data.UpdateIntervalHours = int(v)
			intervalChanged = true
		}
	}
	if list, ok := m["auto_update_containers"].([]any); ok {
		nu := map[string]bool{}
		for _, item := range list {
			if s, ok2 := item.(string); ok2 {
				nu[s] = true
			}
		}
		a.Store.Data.AutoUpdate = nu
	}
	if v, ok := m["auto_prune_enabled"].(bool); ok {
		a.Store.Data.AutoPruneEnabled = v
	}
	if v, ok := m["auto_prune_all"].(bool); ok {
		a.Store.Data.AutoPruneAll = v
	}
	if v, ok := m["auto_prune_interval_hours"].(float64); ok && int(v) > 0 {
		a.Store.Data.AutoPruneIntervalHours = int(v)
	}
	a.Store.mu.Unlock()

	if intervalChanged {
		a.resetUpdateSchedule()
	}
	a.Store.Save()
	writeJSON(w, 200, map[string]any{"success": true, "message": "配置已保存"})
}

// resetUpdateSchedule 间隔被改动后重排下一次运行时间，让新设置不用等一整轮才生效。
// 同样先把 interval 算出来再加锁（见 SetAutoUpdate 的说明）。
func (a *App) resetUpdateSchedule() {
	iv := a.updateInterval()
	a.Store.mu.Lock()
	defer a.Store.mu.Unlock()
	base, ok := parseStoreTime(a.Store.Data.AutoUpdateLastRun)
	if !ok {
		base = time.Now()
	}
	a.Store.Data.AutoUpdateNextRun = base.Add(iv).Format(timeLayout)
}

func (a *App) updateApply(w http.ResponseWriter, r *http.Request, name string) {
	// 异步执行：拉镜像可能耗时几分钟，HTTP 请求立刻返回，进度走 WebSocket。
	// 三种结局分别落三种日志，方便在「系统设置 → 操作日志」里分辨
	// 「真更新了」/「检查了但没必要动」/「失败了」。
	if !a.Tasks.Start("update:"+name, func(progress func(msg string, status string, pct int)) {
		changed, err := a.recreateWithLatestImage(name, progress)
		switch {
		case err != nil:
			a.Logs.Add("ERROR", "容器 ["+name+"] 更新失败: "+err.Error(), "realtime")
		case changed:
			a.Logs.Add("SUCCESS", "容器 ["+name+"] 已更新（旧镜像备份为 -bak 标签）", "realtime")
		default:
			a.Logs.Add("INFO", "容器 ["+name+"] 镜像已是最新，跳过重建（未重启）", "realtime")
		}
	}) {
		fail(w, 429, "该容器的更新任务已在进行中")
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "容器 [" + name + "] 更新任务已启动"})
}

// updateAutoToggle POST /api/updates/{name}/auto {enabled}
// 容器页的「收藏」按钮与更新中心的「自动」开关都打到这里。
func (a *App) updateAutoToggle(w http.ResponseWriter, r *http.Request, name string) {
	m := readBody(r)
	enabled := bodyBool(m, "enabled")
	next := a.SetAutoUpdate(name, enabled)
	ok(w, map[string]any{"enabled": enabled, "auto_update_next_run": next})
}

// SetAutoUpdate 写入收藏状态，并维护调度器的下次运行时间，返回最新的 NextRun。
// 规则：从「没有收藏」变成「有收藏」时开始计时；收藏清空时清掉 NextRun，
// 免得界面上挂着一个永远不会发生的时刻。
//
// 注意：interval 必须在加写锁之前算好 —— updateInterval() 内部要取读锁，
// 而 sync.RWMutex 不可重入，锁内调用会直接死锁。
func (a *App) SetAutoUpdate(name string, enabled bool) string {
	iv := a.updateInterval()
	a.Store.mu.Lock()
	a.Store.Data.AutoUpdate[name] = enabled
	any := false
	for _, en := range a.Store.Data.AutoUpdate {
		if en {
			any = true
			break
		}
	}
	switch {
	case !any:
		a.Store.Data.AutoUpdateNextRun = ""
	case a.Store.Data.AutoUpdateNextRun == "":
		a.Store.Data.AutoUpdateNextRun = time.Now().Add(iv).Format(timeLayout)
	}
	next := a.Store.Data.AutoUpdateNextRun
	a.Store.mu.Unlock()
	a.Store.Save()
	return next
}

// ---------- 重建式更新：拉镜像 → 判有无变化 → 备份旧镜像 → 停 → 删 → 建 → 启 ----------

// imageIDOf 取某个镜像引用（repo:tag 或 sha256:xxx）对应的镜像 ID。
func (a *App) imageIDOf(ref string) string {
	var out struct {
		ID string `json:"Id"`
	}
	if err := a.Docker.doJSON("GET", "/images/"+ref+"/json", nil, nil, &out); err != nil {
		return ""
	}
	return out.ID
}

// tagImage 给指定镜像 ID 补一个 repo:tag 标签。
func (a *App) tagImage(id, repo, tag string) error {
	resp, err := a.Docker.do("POST", "/images/"+id+"/tag", url.Values{"repo": {repo}, "tag": {tag}}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("%s", truncate(string(b), 200))
	}
	return nil
}

// bakTagFor 旧镜像的备份标签名。固定为 <tag>-bak，因此每个容器镜像只保留
// **一个**回滚点（下一次更新会覆盖它），不会无限堆积。
func bakTagFor(tag string) string { return tag + "-bak" }

// isBakTag 判断一个 RepoTag 是否是本面板打上的回滚备份标签。
func isBakTag(repoTag string) bool { return strings.HasSuffix(repoTag, "-bak") }

// streamError 从 Docker 的流式 JSON 响应里提取错误信息。
// 形如 {"errorDetail":{"message":"..."},"error":"..."}，
// 一行一个对象、可能夹杂正常的 pull 进度行，所以逐行扫、取最后一个 error。
func streamError(raw []byte) string {
	msg := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, `"error"`) {
			continue
		}
		var m struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if m.ErrorDetail.Message != "" {
			msg = m.ErrorDetail.Message
		} else if m.Error != "" {
			msg = m.Error
		}
	}
	if msg == "" {
		if len(raw) == 0 {
			return "daemon 未返回任何内容"
		}
		msg = truncate(strings.TrimSpace(string(raw)), 200)
	}
	return msg
}

// createContainerBody 组装 POST /containers/create 的请求体。
//
// ★ Docker 期望的是「容器 Config 的字段**平铺在顶层**」+「HostConfig 嵌套」+ 顶层带 Image：
//
//	{"Image":"nginx:alpine","Cmd":[...],"Env":[...],"HostConfig":{...}}
//
// 早期版本发的是 {"Config":{...},"HostConfig":{...}}，daemon 只会回
// `config cannot be empty in order to create a container` ——
// 也就是说 v1.0.0～v1.0.2 的「一键更新」从来没成功建出过容器。
// 这里抽成纯函数并由 TestCreateContainerBody 锁住形状，防止再改回去。
func createContainerBody(config, hostCfg map[string]any, image string) map[string]any {
	// create 不接受的只读字段（由 daemon 自己填，传了会 400）
	for _, k := range []string{"Hostname", "Domainname", "AttachStdin", "AttachStdout", "AttachStderr",
		"Tty", "OpenStdin", "StdinOnce", "NetworkDisabled", "MacAddress",
		"StopTimeout", "Healthcheck"} {
		delete(config, k)
	}
	delete(hostCfg, "ContainerIDFile")
	delete(hostCfg, "AutoRemove")

	body := map[string]any{}
	for k, v := range config {
		body[k] = v
	}
	body["Image"] = image
	body["HostConfig"] = hostCfg
	return body
}

// recreateWithLatestImage 用 registry 上的最新镜像重建容器。
//
// 返回值 changed=false 表示「镜像本来就是最新的，没有重建」—— 调用方据此区分
// 「真的更新了」和「检查了一遍但没必要动」。
//
// 实际流程（与「先停→再拉」的顺序不同，这里把拉取放在最前面，目的是把停机时间压到最小）：
//
//  1. inspect 容器，记录当前镜像 ID（旧镜像）以及原始 Config / HostConfig
//  2. 拉取 repo:tag —— 此时容器仍在运行，服务不中断
//  3. 新旧镜像 ID 相同 → 镜像本来就是最新的，**完全跳过重建**（不重启、不闪断）
//  4. 新旧镜像不同 → 给旧镜像补一个 <repo>:<tag>-bak 标签，保留回滚点
//  5. 停止容器 → 删除容器 → 用原配置重建 → 启动
//
// 注意第 5 步用的是「原容器配置」而不是 compose 文件，所以面板不依赖 compose，
// 但反过来：如果容器是由 compose 管理的，重建会脱离 compose 的元数据
// （compose 下次 up 时会按自己的文件重新接管）。
func (a *App) recreateWithLatestImage(name string, progress func(msg string, status string, pct int)) (bool, error) {
	var insp map[string]any
	if err := a.Docker.doJSON("GET", "/containers/"+name+"/json", nil, nil, &insp); err != nil {
		return false, fmt.Errorf("inspect: %w", err)
	}
	config, _ := insp["Config"].(map[string]any)
	hostCfg, _ := insp["HostConfig"].(map[string]any)
	image, _ := config["Image"].(string)
	repo, tag := parseImage(image)
	oldImageID, _ := insp["Image"].(string)

	progress("拉取镜像 "+image, "Downloading", 10)
	resp, err := a.Docker.do("POST", "/images/create", url.Values{"fromImage": {repo}, "tag": {tag}}, nil)
	if err != nil {
		return false, fmt.Errorf("pull: %w", err)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		// /images/create 是流式响应，真正的失败原因在最后一行 JSON 的 error 字段里；
		// 只看 HTTP 状态码会得到一句没用的「HTTP 500」。
		// 注意：拉取失败时容器**原封不动**，不会因为更新失败而停服。
		return false, fmt.Errorf("拉取镜像失败 (HTTP %d): %s", resp.StatusCode, truncate(streamError(raw), 300))
	}
	progress("镜像拉取完成", "Pulled", 40)

	// 镜像 ID 没变 = registry 上没有新版本，直接收工。
	// 这一步让「定时自动更新」不再无谓地停掉并重建容器。
	newImageID := a.imageIDOf(repo + ":" + tag)
	if oldImageID != "" && newImageID != "" && newImageID == oldImageID {
		progress("镜像已是最新，跳过重建", "Skipped", 100)
		return false, nil
	}

	// 保留回滚点：把旧镜像钉在一个固定标签上（覆盖上一次的备份）。
	if oldImageID != "" {
		bak := bakTagFor(tag)
		if err := a.tagImage(oldImageID, repo, bak); err != nil {
			progress("旧镜像备份标签失败（不影响本次更新）", "Warn", 46)
			a.Logs.Add("WARNING", fmt.Sprintf("容器 [%s] 旧镜像备份标签失败: %v", name, err), "realtime")
		} else {
			progress("旧镜像已备份为 "+repo+":"+bak, "Backup", 48)
		}
	}

	progress("停止容器", "Stopping", 55)
	a.Docker.ContainerAction(name, "stop", 15)
	progress("删除旧容器", "Removing", 65)
	a.Docker.RemoveContainer(name, true)

	// 清理 create 不接受的只读字段，然后交给 createContainerBody 组包
	createBody := createContainerBody(config, hostCfg, image)
	var created struct {
		ID string `json:"Id"`
	}
	if err := a.Docker.doJSON("POST", "/containers/create", url.Values{"name": {name}}, createBody, &created); err != nil {
		return false, fmt.Errorf("创建新容器失败: %w", err)
	}
	progress("启动新容器", "Starting", 85)
	if err := a.Docker.ContainerAction(created.ID, "start", 0); err != nil {
		return false, fmt.Errorf("启动失败: %w", err)
	}
	a.Store.mu.Lock()
	a.Store.Data.HasUpdate[name] = false
	a.Store.Data.UpdateStatus[name] = "up-to-date"
	a.Store.mu.Unlock()
	a.Store.Save()
	a.Hub.Broadcast("containers_updated", map[string]any{})
	return true, nil
}

// ---------- 调度循环：自动更新容器 + 定时清理未使用镜像 ----------

// autoUpdateLoop 自动更新 / 自动清理镜像的统一调度循环。
//
// 设计要点：
//   - 每 30 秒 tick 一次，而不是 sleep 整个间隔。这样「改间隔 / 收藏或取消容器 /
//     开关自动清理」都能在 30 秒内生效，不必重启面板。
//   - 是否该跑由 Store 里的 NextRun 决定，NextRun 同时透给界面展示「下次运行时间」。
//   - 面板刚启动（NextRun 为空）时只排期、不立即执行，避免重启就触发一轮更新。
func autoUpdateLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		app.runAutoUpdateIfDue()
		app.runAutoPruneIfDue()
		<-ticker.C
	}
}

// containerRunning 判断容器是否存在且处于 running（自动更新只处理运行中的容器）。
func (a *App) containerRunning(name string) bool {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return false
	}
	for _, c := range cs {
		if containerName(c) == name {
			return c.State == "running"
		}
	}
	return false
}

func (a *App) runAutoUpdateIfDue() {
	targets := a.autoTargets()
	iv := a.updateInterval()

	a.Store.mu.RLock()
	nextRaw := a.Store.Data.AutoUpdateNextRun
	a.Store.mu.RUnlock()

	if len(targets) == 0 {
		if nextRaw != "" {
			a.Store.mu.Lock()
			a.Store.Data.AutoUpdateNextRun = ""
			a.Store.mu.Unlock()
			a.Store.Save()
		}
		return
	}
	next, ok := parseStoreTime(nextRaw)
	now := time.Now()
	if !ok {
		a.setAutoUpdateTimes(time.Time{}, now.Add(iv))
		return
	}
	if now.Before(next) {
		return
	}

	a.Logs.Add("INFO", fmt.Sprintf("自动更新开始，已收藏 %d 个容器", len(targets)), "realtime")
	for _, name := range targets {
		// 每处理一个都重新读一次开关：用户中途取消收藏即时生效
		a.Store.mu.RLock()
		on := a.Store.Data.AutoUpdate[name]
		a.Store.mu.RUnlock()
		if !on {
			continue
		}
		if !a.containerRunning(name) {
			a.Logs.Add("INFO", "自动更新跳过 ["+name+"]：容器不存在或未运行", "realtime")
			continue
		}
		key := "update:" + name
		if !a.Tasks.Acquire(key) {
			a.Logs.Add("INFO", "自动更新跳过 ["+name+"]：该容器已有更新任务在跑", "realtime")
			continue
		}
		// 同步串行执行（本函数已在独立 goroutine 中），避免多个容器同时停服
		changed, err := a.recreateWithLatestImage(name, updateProgress(key))
		a.Tasks.Release(key)
		switch {
		case err != nil:
			a.Logs.Add("ERROR", "自动更新失败 ["+name+"]: "+err.Error(), "realtime")
			a.tgNotify("update", "❌ <b>自动更新失败</b>\n\n容器：<code>"+
				escapeXML(name)+"</code>\n原因："+escapeXML(err.Error()))
		case changed:
			a.Logs.Add("SUCCESS", "自动更新完成 ["+name+"]（旧镜像备份为 -bak）", "realtime")
			a.tgNotify("update", "✅ <b>自动更新完成</b>\n\n容器：<code>"+
				escapeXML(name)+"</code>\n旧镜像已打上 <code>-bak</code> 回滚标签")
		default:
			a.Logs.Add("INFO", "自动更新 ["+name+"]：镜像已是最新，未重启容器", "realtime")
		}
		time.Sleep(3 * time.Second)
	}
	finished := time.Now()
	a.setAutoUpdateTimes(finished, finished.Add(a.updateInterval()))
}

func (a *App) setAutoUpdateTimes(lastRun, nextRun time.Time) {
	a.Store.mu.Lock()
	if !lastRun.IsZero() {
		a.Store.Data.AutoUpdateLastRun = lastRun.Format(timeLayout)
	}
	a.Store.Data.AutoUpdateNextRun = nextRun.Format(timeLayout)
	a.Store.mu.Unlock()
	a.Store.Save()
}

// runAutoPruneIfDue 到点则清理未被任何容器使用的镜像。
func (a *App) runAutoPruneIfDue() {
	a.Store.mu.RLock()
	enabled := a.Store.Data.AutoPruneEnabled
	all := a.Store.Data.AutoPruneAll
	nextRaw := a.Store.Data.AutoPruneNextRun
	a.Store.mu.RUnlock()

	if !enabled {
		if nextRaw != "" {
			a.Store.mu.Lock()
			a.Store.Data.AutoPruneNextRun = ""
			a.Store.mu.Unlock()
			a.Store.Save()
		}
		return
	}
	iv := a.pruneInterval()
	next, ok := parseStoreTime(nextRaw)
	now := time.Now()
	if !ok {
		a.setAutoPruneTimes(time.Time{}, now.Add(iv), -1)
		return
	}
	if now.Before(next) {
		return
	}

	rep, err := a.PruneImages(all, true)
	if err != nil {
		a.Logs.Add("ERROR", "自动清理镜像失败: "+err.Error(), "realtime")
	} else {
		a.Logs.Add("SUCCESS", fmt.Sprintf("自动清理镜像完成：删除 %d 个，约释放 %s",
			rep.Deleted, humanBytes(rep.Freed)), "realtime")
	}
	finished := time.Now()
	a.setAutoPruneTimes(finished, finished.Add(iv), rep.Freed)
}

func (a *App) setAutoPruneTimes(lastRun, nextRun time.Time, freed int64) {
	a.Store.mu.Lock()
	if !lastRun.IsZero() {
		a.Store.Data.AutoPruneLastRun = lastRun.Format(timeLayout)
	}
	a.Store.Data.AutoPruneNextRun = nextRun.Format(timeLayout)
	if freed >= 0 {
		a.Store.Data.AutoPruneLastFreed = freed
	}
	a.Store.mu.Unlock()
	a.Store.Save()
}

// ---------- 镜像清理 ----------

type pruneReport struct {
	Deleted int      `json:"deleted"`
	Freed   int64    `json:"freed"`
	Skipped []string `json:"skipped"`
}

// pruneCandidates 挑出可以删除的镜像 ID。
//
//	all=false → 只挑「悬空镜像」（没有任何 RepoTags 的中间层）
//	all=true  → 没有被任何容器引用的都挑上
//	keepBak=true 时跳过带 -bak 后缀的回滚镜像（两种模式都生效）
//
// 单独抽成纯函数是为了能脱离 Docker daemon 做单测。
func pruneCandidates(imgs []DockerImage, inUse map[string]bool, all, keepBak bool) (ids []string, skipped []string) {
	ids, skipped = []string{}, []string{}
	for _, img := range imgs {
		if inUse[img.ID] {
			continue
		}
		dangling := len(img.RepoTags) == 0
		hasBak := false
		for _, rt := range img.RepoTags {
			if rt == "<none>:<none>" {
				dangling = true
			}
			if isBakTag(rt) {
				hasBak = true
			}
		}
		if !all && !dangling {
			continue
		}
		if keepBak && hasBak {
			skipped = append(skipped, strings.Join(img.RepoTags, ",")+"（回滚镜像，保留）")
			continue
		}
		ids = append(ids, img.ID)
	}
	return ids, skipped
}

// PruneImages 清理未被任何容器使用的镜像。
//
//	all=false → 只清「悬空镜像」（没有 tag 的中间层，安全）
//	all=true  → 连有 tag 但没被任何容器使用的镜像一起清
//
// 两种模式都会保留 <tag>-bak 回滚镜像（面板更新时给旧镜像打的备份标签），
// 避免自动清理把回滚点也一起删掉。
//
// 这里不用 Docker 的 POST /images/prune，而是自己挑选 + 逐个删除，
// 因为 prune 接口无法表达「排除 *-bak」这个条件。
func (a *App) PruneImages(all, keepBak bool) (pruneReport, error) {
	rep := pruneReport{Skipped: []string{}}
	imgs, err := a.Docker.ListImages()
	if err != nil {
		return rep, err
	}
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return rep, err
	}
	inUse := map[string]bool{}
	for _, c := range cs {
		if c.ImageID != "" {
			inUse[c.ImageID] = true
		}
	}
	ids, skipped := pruneCandidates(imgs, inUse, all, keepBak)
	rep.Skipped = skipped

	before := a.systemLayersSize()
	for _, id := range ids {
		if err := a.removeImage(id, false); err != nil {
			rep.Skipped = append(rep.Skipped, id[:min(12, len(id))])
			continue
		}
		rep.Deleted++
	}
	after := a.systemLayersSize()
	if before > after {
		rep.Freed = before - after
	}
	return rep, nil
}

// systemLayersSize 取镜像层占用总字节（/system/df 的 LayersSize），
// 用它做清理前后差值，比累加被删镜像的 Size 准确（层是共享的）。
func (a *App) systemLayersSize() int64 {
	var out struct {
		LayersSize int64 `json:"LayersSize"`
	}
	if err := a.Docker.doJSON("GET", "/system/df", nil, nil, &out); err != nil {
		return 0
	}
	return out.LayersSize
}

// handlePruneImages POST /api/images/prune?all=1 —— 手动清理（更新中心的按钮）
func (a *App) handlePruneImages(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "1"
	rep, err := a.PruneImages(all, true)
	if err != nil {
		fail(w, 500, "清理失败: "+err.Error())
		return
	}
	a.Logs.Add("SUCCESS", fmt.Sprintf("镜像清理完成：删除 %d 个，约释放 %s", rep.Deleted, humanBytes(rep.Freed)), "realtime")
	ok(w, map[string]any{"deleted": rep.Deleted, "freed": rep.Freed, "skipped": rep.Skipped})
}

// humanBytes 后端日志里用的体积格式化（前端另有 UI.fmtBytes）。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	f := float64(n)
	i := -1
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
