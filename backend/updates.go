package main

// 镜像更新检查 / 容器更新 / 自动更新调度
import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

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

// registryDigest 查询远程 manifest digest（支持 docker.io / 常见镜像源）
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
	// 获取 token（匿名 pull）
	tokURL := fmt.Sprintf("https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s:pull", scopeRepo)
	if host != "registry-1.docker.io" {
		// 非官方源尝试直接无 token 请求
		tokURL = ""
	}
	client := &http.Client{Timeout: 20 * time.Second}
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, scopeRepo, tag), nil)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json,application/vnd.oci.image.index.v1+json,application/vnd.oci.image.manifest.v1+json")
	if tokURL != "" {
		tr, err := client.Get(tokURL)
		if err == nil {
			var tj struct {
				Token string `json:"token"`
			}
			json.NewDecoder(tr.Body).Decode(&tj)
			tr.Body.Close()
			if tj.Token != "" {
				req.Header.Set("Authorization", "Bearer "+tj.Token)
			}
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	dg := resp.Header.Get("Docker-Content-Digest")
	if dg == "" {
		return "", fmt.Errorf("no digest header")
	}
	return dg, nil
}

// handleCheckUpdates POST /api/check-updates
func (a *App) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	specific := bodyStr(m, "container_name")
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	imgs, _ := a.Docker.ListImages(false)
	localDigest := map[string]string{}
	for _, im := range imgs {
		for _, rd := range im.RepoDigests {
			// "nginx@sha256:xxx"
			parts := strings.SplitN(rd, "@", 2)
			if len(parts) == 2 {
				localDigest[parts[0]] = parts[1]
			}
		}
	}
	results := []updateResult{}
	for _, c := range cs {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		if specific != "" && name != specific {
			continue
		}
		repo, tag := parseImage(c.Image)
		res := updateResult{
			ContainerID: c.ID[:12], ContainerName: name, Image: c.Image,
			Status: "unknown", CheckMethod: "online",
		}
		// 本地 digest
		if ld, okk := localDigest[repo]; okk {
			res.LocalDigest = ld
		}
		// 远端 digest（只查 latest 与固定 tag）
		dg, err := registryDigest(c.Image)
		if err != nil {
			res.Status = "unknown"
		} else {
			res.RemoteDigest = dg
			// 比较镜像 ID 或 digest
			localImgID := strings.TrimPrefix(c.ImageID, "sha256:")
			if dg != "" && res.LocalDigest != "" {
				same := strings.HasSuffix(res.LocalDigest, strings.TrimPrefix(dg, "sha256:")) ||
					strings.HasSuffix(dg, strings.TrimPrefix(res.LocalDigest, "sha256:"))
				if same {
					res.Status = "up-to-date"
				} else {
					res.Status = "updatable"
					res.HasUpdate = true
				}
			} else if repo == c.Image && tag == "latest" {
				// 没有本地 repoDigest 时退化为比较镜像 ID 与远端 manifest（不可靠）→ unknown
				_ = localImgID
				res.Status = "unknown"
			}
		}
		results = append(results, res)
		// 写入存储
		a.Store.mu.Lock()
		a.Store.Data.HasUpdate[name] = res.HasUpdate
		a.Store.Data.UpdateStatus[name] = res.Status
		a.Store.mu.Unlock()
	}
	a.Store.mu.Lock()
	a.Store.Data.LastCheck = a.Store.Now()
	a.Store.mu.Unlock()
	a.Store.Save()
	a.Logs.Add("SUCCESS", fmt.Sprintf("✅ 检查完成: 共检查 %d 个容器，发现 %d 个可更新", len(results), countUpdatable(results)), "realtime")
	writeJSON(w, 200, map[string]any{"success": true, "data": results})
}

func countUpdatable(rs []updateResult) int {
	n := 0
	for _, r := range rs {
		if r.HasUpdate {
			n++
		}
	}
	return n
}

// handleToggleAutoUpdate POST /api/toggle-auto-update
func (a *App) handleToggleAutoUpdate(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	name := bodyStr(m, "container_name")
	if name == "" {
		fail(w, 400, "缺少容器名称")
		return
	}
	enabled := bodyBool(m, "enabled")
	a.Store.mu.Lock()
	a.Store.Data.AutoUpdate[name] = enabled
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, nil)
}

// startUpdateTask 提交容器更新后台任务，返回 false 表示已在进行
func (a *App) startUpdateTask(name string) error {
	if !a.Tasks.Start("update:"+name, func(progress func(msg string, status string, pct int)) {
		if err := a.recreateWithLatestImage(name, progress); err != nil {
			a.Logs.Add("ERROR", "❌ 容器 ["+name+"] 更新失败: "+err.Error(), "realtime")
		} else {
			a.Logs.Add("SUCCESS", "✅ 容器 ["+name+"] 更新成功", "realtime")
		}
	}) {
		return fmt.Errorf("该容器的更新任务已在进行中")
	}
	return nil
}

// handleUpdateContainer POST /api/update-container
func (a *App) handleUpdateContainer(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	name := bodyStr(m, "container_name")
	if name == "" {
		fail(w, 400, "缺少容器名称")
		return
	}
	if err := a.startUpdateTask(name); err != nil {
		fail(w, 429, err.Error())
		return
	}
	okMsg(w, "容器 ["+name+"] 更新任务已启动")
}

// recreateWithLatestImage 拉镜像→停→删→建→启
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
	q := url.Values{"fromImage": {repo}, "tag": {tag}}
	resp, err := a.Docker.do("POST", "/images/create", q, nil)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("拉取镜像失败 (HTTP %d)", resp.StatusCode)
	}
	progress("镜像拉取完成", "Pulled", 40)

	// 重新 inspect 获取新 ImageID
	var newImg struct {
		ID string `json:"Id"`
	}
	a.Docker.doJSON("GET", "/images/"+image+"/json", nil, nil, &newImg)

	progress("停止容器", "Stopping", 55)
	a.Docker.ContainerAction(name, "stop", 15)
	progress("删除旧容器", "Removing", 65)
	a.Docker.RemoveContainer(name, true, false)

	// 清理 create 不接受的字段
	cleanConfig(config)
	cleanHostConfig(hostCfg)
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
	_ = newImg
	a.Store.mu.Lock()
	a.Store.Data.HasUpdate[name] = false
	a.Store.Data.UpdateStatus[name] = "up-to-date"
	a.Store.mu.Unlock()
	a.Store.Save()
	a.Hub.Broadcast("containers_updated", map[string]any{})
	return nil
}

// cleanConfig 移除 create 不接受的只读字段
func cleanConfig(cfg map[string]any) {
	for _, k := range []string{"Hostname", "Domainname", "AttachStdin", "AttachStdout", "AttachStderr",
		"Tty", "OpenStdin", "StdinOnce", "Image", "NetworkDisabled", "MacAddress",
		"StopTimeout", "Healthcheck"} {
		delete(cfg, k)
	}
	// Hostname 若非空保留会导致 network 冲突，删除
	delete(cfg, "Hostname")
}

func cleanHostConfig(hc map[string]any) {
	for _, k := range []string{"Binds2", "ContainerIDFile", "LogConfig", "NetworkMode", "PortBindings", "RestartPolicy", "AutoRemove", "VolumeDriver", "OomScoreAdj", "PidMode", "IpcMode", "Cgroup", "Links", "PublishAllPorts", "ReadonlyRootfs", "SecurityOpt", "UTSMode", "UsernsMode", "ShmSize", "Runtime", "ConsoleSize", "Isolation", "CpuShares", "Memory", "NanoCpus", "CgroupParent", "BlkioWeight", "CpuPeriod", "CpuQuota", "CpusetCpus", "CpusetMems", "Devices", "DiskQuota", "KernelMemory", "MemoryReservation", "MemorySwap", "MemorySwappiness", "OomKillDisable", "PidsLimit", "Ulimits", "CpuCount", "CpuPercent", "IOMaximumIOps", "IOMaximumBandwidth", "Mounts", "Init", "CgroupnsMode"} {
		// 注意：多数字段 create 是接受的；只删确定非法的
		_ = k
	}
	// 只移除已知的非法字段
	for _, k := range []string{"ContainerIDFile", "AutoRemove"} {
		delete(hc, k)
	}
}

// ---------- 自动更新调度 ----------

func autoUpdateLoop() {
	for {
		app.Store.mu.RLock()
		days := app.Store.Data.UpdateIntervalDays
		hours := app.Store.Data.UpdateIntervalHours
		anyEnabled := len(app.Store.Data.AutoUpdate) > 0
		app.Store.mu.RUnlock()
		if anyEnabled {
			interval := time.Duration(days*24+hours) * time.Hour
			if interval < time.Hour {
				interval = 24 * 7 * time.Hour
			}
			runAutoUpdate()
			time.Sleep(interval)
		} else {
			time.Sleep(30 * time.Minute)
		}
	}
}

func runAutoUpdate() {
	app.Store.mu.RLock()
	targets := []string{}
	for name, enabled := range app.Store.Data.AutoUpdate {
		if enabled {
			targets = append(targets, name)
		}
	}
	app.Store.mu.RUnlock()
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
}

// ---------- 任务管理器（防并发） ----------

type TaskManager struct {
	mu       sync.Mutex
	running  map[string]bool
}

func NewTaskManager() *TaskManager {
	return &TaskManager{running: map[string]bool{}}
}

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

var _ = sha256.New
