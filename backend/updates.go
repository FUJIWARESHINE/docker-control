package main

// 更新中心：registry digest 对比检查 / 重建式容器更新 / 自动更新调度 / 防并发任务管理
import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

func (tm *TaskManager) Start(key string, fn func(progress func(msg string, status string, pct int))) bool {
	tm.mu.Lock()
	if tm.running[key] {
		tm.mu.Unlock()
		return false
	}
	tm.running[key] = true
	tm.mu.Unlock()
	progress := func(msg string, status string, pct int) {
		app.Hub.Broadcast("update_progress", map[string]any{
			"container": strings.TrimPrefix(key, "update:"),
			"message":   msg, "status": status, "percentage": pct,
		})
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				app.Logs.Add("ERROR", fmt.Sprintf("任务异常: %v", r), "realtime")
			}
			tm.mu.Lock()
			delete(tm.running, key)
			tm.mu.Unlock()
		}()
		fn(progress)
	}()
	return true
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
	a.Store.mu.Unlock()
	a.Store.Save()
	return results, nil
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
		writeJSON(w, 200, map[string]any{"success": true, "data": map[string]any{
			"update_interval_days":   a.Store.Data.UpdateIntervalDays,
			"update_interval_hours":  a.Store.Data.UpdateIntervalHours,
			"auto_update_containers": autoList,
			"last_check":             a.Store.Data.LastCheck,
		}})
		a.Store.mu.RUnlock()
		return
	}
	m := readBody(r)
	a.Store.mu.Lock()
	if v, ok := m["update_interval_days"].(float64); ok {
		a.Store.Data.UpdateIntervalDays = int(v)
	}
	if v, ok := m["update_interval_hours"].(float64); ok {
		a.Store.Data.UpdateIntervalHours = int(v)
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
	a.Store.mu.Unlock()
	a.Store.Save()
	writeJSON(w, 200, map[string]any{"success": true, "message": "配置已保存"})
}

func (a *App) updateApply(w http.ResponseWriter, r *http.Request, name string) {
	if !a.Tasks.Start("update:"+name, func(progress func(msg string, status string, pct int)) {
		if err := a.recreateWithLatestImage(name, progress); err != nil {
			a.Logs.Add("ERROR", "容器 ["+name+"] 更新失败: "+err.Error(), "realtime")
		} else {
			a.Logs.Add("SUCCESS", "容器 ["+name+"] 更新成功", "realtime")
		}
	}) {
		fail(w, 429, "该容器的更新任务已在进行中")
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "容器 [" + name + "] 更新任务已启动"})
}

func (a *App) updateAutoToggle(w http.ResponseWriter, r *http.Request, name string) {
	m := readBody(r)
	enabled := bodyBool(m, "enabled")
	a.Store.mu.Lock()
	a.Store.Data.AutoUpdate[name] = enabled
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, nil)
}

// ---------- 重建式更新：拉镜像 → 停 → 删 → 建 → 启 ----------

func (a *App) recreateWithLatestImage(name string, progress func(msg string, status string, pct int)) error {
	var insp map[string]any
	if err := a.Docker.doJSON("GET", "/containers/"+name+"/json", nil, nil, &insp); err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	config, _ := insp["Config"].(map[string]any)
	hostCfg, _ := insp["HostConfig"].(map[string]any)
	image, _ := config["Image"].(string)
	repo, tag := parseImage(image)

	progress("拉取镜像 "+image, "Downloading", 10)
	resp, err := a.Docker.do("POST", "/images/create", url.Values{"fromImage": {repo}, "tag": {tag}}, nil)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("拉取镜像失败 (HTTP %d)", resp.StatusCode)
	}
	progress("镜像拉取完成", "Pulled", 40)

	progress("停止容器", "Stopping", 55)
	a.Docker.ContainerAction(name, "stop", 15)
	progress("删除旧容器", "Removing", 65)
	a.Docker.RemoveContainer(name, true)

	// 清理 create 不接受的只读字段
	for _, k := range []string{"Hostname", "Domainname", "AttachStdin", "AttachStdout", "AttachStderr",
		"Tty", "OpenStdin", "StdinOnce", "Image", "NetworkDisabled", "MacAddress",
		"StopTimeout", "Healthcheck"} {
		delete(config, k)
	}
	delete(hostCfg, "ContainerIDFile")
	delete(hostCfg, "AutoRemove")

	createBody := map[string]any{"Config": config, "HostConfig": hostCfg}
	var created struct {
		ID string `json:"Id"`
	}
	if err := a.Docker.doJSON("POST", "/containers/create", url.Values{"name": {name}}, createBody, &created); err != nil {
		return fmt.Errorf("创建新容器失败: %w", err)
	}
	progress("启动新容器", "Starting", 85)
	if err := a.Docker.ContainerAction(created.ID, "start", 0); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	a.Store.mu.Lock()
	a.Store.Data.HasUpdate[name] = false
	a.Store.Data.UpdateStatus[name] = "up-to-date"
	a.Store.mu.Unlock()
	a.Store.Save()
	a.Hub.Broadcast("containers_updated", map[string]any{})
	return nil
}

// ---------- 自动更新调度 ----------

func autoUpdateLoop() {
	for {
		app.Store.mu.RLock()
		interval := time.Duration(app.Store.Data.UpdateIntervalDays*24+app.Store.Data.UpdateIntervalHours) * time.Hour
		targets := []string{}
		for name, enabled := range app.Store.Data.AutoUpdate {
			if enabled {
				targets = append(targets, name)
			}
		}
		app.Store.mu.RUnlock()
		if len(targets) > 0 {
			if interval < time.Hour {
				interval = 24 * 7 * time.Hour
			}
			for _, name := range targets {
				app.Tasks.Start("update:"+name, func(progress func(msg string, status string, pct int)) {
					if err := app.recreateWithLatestImage(name, progress); err != nil {
						app.Logs.Add("ERROR", "自动更新失败 ["+name+"]: "+err.Error(), "realtime")
					} else {
						app.Logs.Add("SUCCESS", "自动更新成功 ["+name+"]", "realtime")
					}
				})
				time.Sleep(5 * time.Second)
			}
			time.Sleep(interval)
		} else {
			time.Sleep(30 * time.Minute)
		}
	}
}
