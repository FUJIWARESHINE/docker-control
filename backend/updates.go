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

// registryDigest 查询远程 manifest digest（docker.io 匿名可读）
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
	client := &http.Client{Timeout: 20 * time.Second}
	req, _ := http.NewRequest("GET", fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, scopeRepo, tag), nil)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json,application/vnd.oci.image.index.v1+json,application/vnd.oci.image.manifest.v1+json")
	if host == "registry-1.docker.io" {
		tr, err := client.Get(fmt.Sprintf("https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s:pull", scopeRepo))
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

// handleCheckUpdates POST /api/updates/check {container_name?}
func (a *App) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	specific := bodyStr(readBody(r), "container_name")
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		fail(w, 500, err.Error())
		return
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
	n := 0
	for _, r := range results {
		if r.HasUpdate {
			n++
		}
	}
	a.Logs.Add("SUCCESS", fmt.Sprintf("更新检查完成: %d 个容器，%d 个可更新", len(results), n), "realtime")
	writeJSON(w, 200, map[string]any{"success": true, "data": results})
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
