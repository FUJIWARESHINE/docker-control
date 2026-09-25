package main

// 系统与偏好：配置 / 别名 / 用户偏好 / API Key / 模板 / 定时任务 / 操作日志 / 系统信息 / 备份
import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

// ---------- 配置 ----------

// handleConfig GET /api/config
func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	writeJSON(w, 200, map[string]any{"success": true, "data": map[string]any{
		"update_interval_days":  a.Store.Data.UpdateIntervalDays,
		"update_interval_hours": a.Store.Data.UpdateIntervalHours,
		"api_enabled":           len(a.Store.Data.APIKeys) > 0,
		"last_check":            a.Store.Data.LastCheck,
	}})
}

// handleAliases GET/PUT /api/aliases
func (a *App) handleAliases(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.Store.mu.RLock()
		defer a.Store.mu.RUnlock()
		ok(w, a.Store.Data.Aliases)
		return
	}
	m := readBody(r)
	a.Store.mu.Lock()
	for k, v := range m {
		a.Store.Data.Aliases[k] = v
	}
	a.Store.mu.Unlock()
	a.Store.Save()
	writeJSON(w, 200, map[string]any{"success": true, "message": "别名已保存"})
}

// handlePrefs GET/PUT /api/prefs
func (a *App) handlePrefs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.Store.mu.RLock()
		defer a.Store.mu.RUnlock()
		ok(w, a.Store.Data.UserPrefs)
		return
	}
	m := readBody(r)
	a.Store.mu.Lock()
	for k, v := range m {
		a.Store.Data.UserPrefs[k] = v
	}
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, nil)
}

// ---------- API Key ----------

// handleAPIKeys GET/POST /api/apikeys
func (a *App) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.Store.mu.RLock()
		defer a.Store.mu.RUnlock()
		writeJSON(w, 200, map[string]any{"success": true, "data": a.Store.Data.APIKeys})
		return
	}
	key := "dc-" + randHex(24)
	a.Store.mu.Lock()
	a.Store.Data.APIKeys = append(a.Store.Data.APIKeys, APIKey{
		ID: randHex(8), Key: key, Enabled: true, CreatedAt: a.Store.Now(),
	})
	a.Store.mu.Unlock()
	a.Store.Save()
	a.Logs.Add("WARNING", "生成新 API Key", "system")
	ok(w, map[string]any{"key": key})
}

func (a *App) apikeyToggle(w http.ResponseWriter, r *http.Request, id string) {
	a.Store.mu.Lock()
	for i := range a.Store.Data.APIKeys {
		if a.Store.Data.APIKeys[i].ID == id {
			a.Store.Data.APIKeys[i].Enabled = !a.Store.Data.APIKeys[i].Enabled
		}
	}
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, nil)
}

func (a *App) apikeyDelete(w http.ResponseWriter, r *http.Request, id string) {
	a.Store.mu.Lock()
	out := a.Store.Data.APIKeys[:0]
	for _, k := range a.Store.Data.APIKeys {
		if k.ID != id {
			out = append(out, k)
		}
	}
	a.Store.Data.APIKeys = out
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, nil)
}

// ---------- 模板 ----------

// handleTemplates GET/POST /api/templates
func (a *App) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.Store.mu.RLock()
		defer a.Store.mu.RUnlock()
		if a.Store.Data.Templates == nil {
			ok(w, []any{})
			return
		}
		ok(w, a.Store.Data.Templates)
		return
	}
	m := readBody(r)
	name := bodyStr(m, "name")
	if name == "" {
		fail(w, 400, "缺少模板名称")
		return
	}
	a.Store.mu.Lock()
	replaced := false
	for i := range a.Store.Data.Templates {
		if tn, _ := a.Store.Data.Templates[i]["name"].(string); tn == name {
			a.Store.Data.Templates[i] = m
			replaced = true
		}
	}
	if !replaced {
		a.Store.Data.Templates = append(a.Store.Data.Templates, m)
	}
	a.Store.mu.Unlock()
	a.Store.Save()
	writeJSON(w, 200, map[string]any{"success": true, "message": "模板已保存"})
}

func (a *App) templateGet(w http.ResponseWriter, r *http.Request, name string) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	for _, t := range a.Store.Data.Templates {
		if tn, _ := t["name"].(string); tn == name {
			ok(w, t)
			return
		}
	}
	fail(w, 404, "模板不存在")
}

func (a *App) templateDelete(w http.ResponseWriter, r *http.Request, name string) {
	a.Store.mu.Lock()
	out := a.Store.Data.Templates[:0]
	for _, t := range a.Store.Data.Templates {
		if tn, _ := t["name"].(string); tn != name {
			out = append(out, t)
		}
	}
	a.Store.Data.Templates = out
	a.Store.mu.Unlock()
	a.Store.Save()
	writeJSON(w, 200, map[string]any{"success": true, "message": "模板已删除"})
}

// ---------- 定时任务 ----------

// handleTasks GET/POST/PUT /api/tasks
func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.Store.mu.RLock()
		tasks := a.Store.Data.Tasks
		if tasks == nil {
			tasks = []Task{}
		}
		a.Store.mu.RUnlock()
		writeJSON(w, 200, map[string]any{"success": true, "tasks": tasks, "data": tasks})
	case http.MethodPost:
		m := readBody(r)
		name := bodyStr(m, "container_name")
		if name == "" {
			name = bodyStr(m, "project_name")
		}
		if name == "" {
			name = bodyStr(m, "name")
		}
		if name == "" {
			fail(w, 400, "缺少任务目标")
			return
		}
		t := Task{
			ID: randHex(8), Type: bodyStr(m, "type"), Name: name,
			Action: bodyStr(m, "action"), Cron: bodyStr(m, "cron_expression"),
			Enabled: true, CreatedAt: a.Store.Now(),
		}
		if t.Type == "" {
			t.Type = "container"
		}
		a.Store.mu.Lock()
		a.Store.Data.Tasks = append(a.Store.Data.Tasks, t)
		a.Store.mu.Unlock()
		a.Store.Save()
		ok(w, t)
	case http.MethodPut:
		m := readBody(r)
		id := bodyStr(m, "id")
		a.Store.mu.Lock()
		for i := range a.Store.Data.Tasks {
			if a.Store.Data.Tasks[i].ID == id {
				if v := bodyStr(m, "action"); v != "" {
					a.Store.Data.Tasks[i].Action = v
				}
				if v := bodyStr(m, "cron_expression"); v != "" {
					a.Store.Data.Tasks[i].Cron = v
				}
				if v, ok := m["enabled"].(bool); ok {
					a.Store.Data.Tasks[i].Enabled = v
				}
			}
		}
		a.Store.mu.Unlock()
		a.Store.Save()
		ok(w, nil)
	default:
		fail(w, 405, "method not allowed")
	}
}

func (a *App) taskDelete(w http.ResponseWriter, r *http.Request, id string) {
	a.Store.mu.Lock()
	out := a.Store.Data.Tasks[:0]
	for _, t := range a.Store.Data.Tasks {
		if t.ID != id {
			out = append(out, t)
		}
	}
	a.Store.Data.Tasks = out
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, nil)
}

func (a *App) taskExecute(w http.ResponseWriter, r *http.Request, id string) {
	a.Store.mu.RLock()
	var target *Task
	for i := range a.Store.Data.Tasks {
		if a.Store.Data.Tasks[i].ID == id {
			target = &a.Store.Data.Tasks[i]
		}
	}
	a.Store.mu.RUnlock()
	if target == nil {
		fail(w, 404, "任务不存在")
		return
	}
	var err error
	switch target.Action {
	case "start", "stop", "restart":
		err = a.Docker.ContainerAction(target.Name, target.Action, 10)
	case "update":
		if !a.Tasks.Start("update:"+target.Name, func(progress func(msg string, status string, pct int)) {
			if e := a.recreateWithLatestImage(target.Name, progress); e != nil {
				a.Logs.Add("ERROR", "任务更新失败 ["+target.Name+"]: "+e.Error(), "realtime")
			}
		}) {
			fail(w, 429, "该容器的更新任务已在进行中")
			return
		}
	default:
		fail(w, 400, "不支持的动作: "+target.Action)
		return
	}
	if err != nil {
		fail(w, 500, "执行失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "任务已执行"})
}

// ---------- 日志 / 系统 / 备份 ----------

// handleLogs GET /api/logs
func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	data := a.Logs.All()
	writeJSON(w, 200, map[string]any{"success": true, "data": data, "count": len(data), "total": len(data)})
}

func (a *App) handleLogsDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=docker-control-logs.txt")
	for _, l := range a.Logs.All() {
		ts, _ := l["timestamp"].(string)
		lv, _ := l["level"].(string)
		msg, _ := l["message"].(string)
		w.Write([]byte(ts + " [" + lv + "] " + msg + "\n"))
	}
}

// handleSystemInfo GET /api/system/info
func (a *App) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]any{"version": Version}
	if v, err := os.ReadFile("/proc/uptime"); err == nil {
		info["proc_uptime"] = strings.TrimSpace(string(v))
	}
	var v struct {
		ServerVersion string `json:"ServerVersion"`
		Os            string `json:"Os"`
		Arch          string `json:"Arch"`
	}
	if err := a.Docker.doJSON("GET", "/version", nil, nil, &v); err == nil {
		info["docker_version"] = v.ServerVersion
		info["docker_os"] = v.Os
		info["docker_arch"] = v.Arch
	}
	ok(w, info)
}

func (a *App) handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	a.Logs.Add("WARNING", "面板重启请求", "system")
	writeJSON(w, 200, map[string]any{"success": true, "message": "重启指令已提交（由容器编排自动拉起）"})
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
}

// handleBackup GET /api/backup —— 下载完整配置备份
func (a *App) handleBackup(w http.ResponseWriter, r *http.Request) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=docker-control-backup.json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	json.NewEncoder(w).Encode(a.Store.Data)
}

// ---------- 杂项 ----------

type busyError struct{ msg string }

func (e busyError) Error() string { return e.msg }

var errBusy = busyError{msg: "该容器的更新任务已在进行中"}
