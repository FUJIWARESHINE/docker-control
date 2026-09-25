package main

// 用户偏好/配置/别名/图标/模板/定时任务/系统日志/API Key/认证
import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Store) dataDir() string {
	return filepath.Dir(s.path)
}

func filepath_join(parts ...string) string {
	return filepath.Join(parts...)
}

func parentDir(p string) string {
	return filepath.Dir(p)
}

// ---------- 认证 ----------

// handleAuthLogin POST /api/auth/login {password, totp_code?}
func (a *App) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	pw := bodyStr(m, "password")
	if pw == "" || !a.Store.CheckPassword(pw) {
		fail(w, 401, "密码错误")
		return
	}
	tk := randHexToken()
	tokens.Lock()
	tokens.m[tk] = time.Now().Unix()
	tokens.Unlock()
	// 同时种 cookie：前端大量 loader 使用裸 fetch（无 Authorization 头）
	http.SetCookie(w, &http.Cookie{
		Name: "dc_token", Value: tk, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: 86400 * 7,
	})
	a.Logs.Add("INFO", "👤 用户登录", "system")
	writeJSON(w, 200, map[string]any{
		"success": true, "token": tk,
		"using_default_password": a.Store.UsingDefaultPassword(),
	})
}

func randHexToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// handleAuthLogout POST /api/auth/logout
func (a *App) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		tk := strings.TrimPrefix(auth, "Bearer ")
		tokens.Lock()
		delete(tokens.m, tk)
		tokens.Unlock()
	}
	// 清除 cookie 会话
	http.SetCookie(w, &http.Cookie{Name: "dc_token", Value: "", Path: "/", MaxAge: -1})
	ok(w, nil)
}

// handleChangePassword POST /api/auth/change-password
func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	oldpw := bodyStr(m, "old_password")
	newpw := bodyStr(m, "new_password")
	if oldpw == "" {
		oldpw = bodyStr(m, "oldPassword")
	}
	if newpw == "" {
		newpw = bodyStr(m, "newPassword")
	}
	if !a.Store.CheckPassword(oldpw) {
		fail(w, 400, "原密码错误")
		return
	}
	if len(newpw) < 6 {
		fail(w, 400, "新密码至少 6 位")
		return
	}
	a.Store.SetPassword(newpw)
	a.Store.Save()
	a.Logs.Add("WARNING", "🔑 密码已修改", "system")
	okMsg(w, "密码修改成功")
}

// ---------- Public（免认证） ----------

// handlePublicTheme GET /api/public/theme
func (a *App) handlePublicTheme(w http.ResponseWriter, r *http.Request) {
	a.Store.mu.RLock()
	theme, _ := a.Store.Data.UserPrefs["theme"].(string)
	mobile, _ := a.Store.Data.UserPrefs["mobile_theme"].(string)
	a.Store.mu.RUnlock()
	if theme == "" {
		theme = "tradis-blue"
	}
	if mobile == "" {
		mobile = "dark"
	}
	writeJSON(w, 200, map[string]any{"success": true, "theme": theme, "mobile_theme": mobile})
}

// handlePublicPasswordStatus GET /api/public/password-status
func (a *App) handlePublicPasswordStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"success": true, "using_default_password": a.Store.UsingDefaultPassword()})
}

// handlePublicVersion GET /api/public/version
func (a *App) handlePublicVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"success": true, "version": a.Version})
}

// ---------- 免责声明 ----------

// handleDisclaimerStatus GET /api/disclaimer/status
func (a *App) handleDisclaimerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"success": true, "agreed_forever": a.Store.Data.AgreedForever})
}

// handleDisclaimerAgree POST /api/disclaimer/agree
func (a *App) handleDisclaimerAgree(w http.ResponseWriter, r *http.Request) {
	a.Store.mu.Lock()
	a.Store.Data.AgreedForever = true
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, nil)
}

// ---------- Config ----------

// handleGetConfig GET /api/config
func (a *App) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	autoList := []string{}
	for name, en := range a.Store.Data.AutoUpdate {
		if en {
			autoList = append(autoList, name)
		}
	}
	writeJSON(w, 200, map[string]any{"success": true, "data": map[string]any{
		"auto_update_enabled":    len(autoList) > 0,
		"update_interval_days":   a.Store.Data.UpdateIntervalDays,
		"update_interval_hours":  a.Store.Data.UpdateIntervalHours,
		"auto_update_containers": autoList,
		"api_enabled":            len(a.Store.Data.APIKeys) > 0,
	}})
}

// handleSaveConfig POST /api/config
func (a *App) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	a.Store.mu.Lock()
	if v, okk := m["update_interval_days"].(float64); okk {
		a.Store.Data.UpdateIntervalDays = int(v)
	}
	if v, okk := m["update_interval_hours"].(float64); okk {
		a.Store.Data.UpdateIntervalHours = int(v)
	}
	if list, okk := m["auto_update_containers"].([]any); okk {
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
	okMsg(w, "配置已保存")
}

// handleAliases GET/POST /api/config/aliases
func (a *App) handleAliases(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
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
	okMsg(w, "别名已保存")
}

// ---------- 用户偏好 ----------

// handleUserPrefsGet GET /api/user-prefs/get
func (a *App) handleUserPrefsGet(w http.ResponseWriter, r *http.Request) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	ok(w, a.Store.Data.UserPrefs)
}

// handleUserPrefsSet POST /api/user-prefs/set
func (a *App) handleUserPrefsSet(w http.ResponseWriter, r *http.Request) {
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

// handleAPIKey GET /api/apikey/list
func (a *App) handleAPIKeyList(w http.ResponseWriter, r *http.Request) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	writeJSON(w, 200, map[string]any{"success": true, "data": a.Store.Data.APIKeys, "keys": a.Store.Data.APIKeys})
}

// handleAPIKeyGenerate POST /api/apikey/generate
func (a *App) handleAPIKeyGenerate(w http.ResponseWriter, r *http.Request) {
	key := "dc-" + randHexToken()
	a.Store.mu.Lock()
	a.Store.Data.APIKeys = append(a.Store.Data.APIKeys, APIKey{
		ID: randHex(8), Key: key, Enabled: true, CreatedAt: a.Store.Now(),
	})
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, map[string]any{"key": key})
}

// handleAPIKeyToggle POST /api/apikey/toggle {id}
func (a *App) handleAPIKeyToggle(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	id := bodyStr(m, "id")
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

// handleAPIKeyDelete POST /api/apikey/delete {id}
func (a *App) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	id := bodyStr(m, "id")
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

// ---------- 系统日志 ----------

type LogRing struct {
	mu   chan struct{}
	data []map[string]any
	max  int
}

func NewLogRing(max int) *LogRing {
	return &LogRing{mu: make(chan struct{}, 1), data: []map[string]any{}, max: max}
}

func (l *LogRing) Add(level, message, typ string) {
	l.mu <- struct{}{}
	l.data = append(l.data, map[string]any{
		"timestamp": time.Now().Format("2006-01-02 15:04:05"),
		"level":     level, "message": message, "type": typ,
	})
	if len(l.data) > l.max {
		l.data = l.data[len(l.data)-l.max:]
	}
	<-l.mu
	app.Hub.Broadcast("log", map[string]any{"level": level, "message": message, "type": typ, "timestamp": time.Now().Format("2006-01-02 15:04:05")})
}

func (l *LogRing) All() []map[string]any {
	l.mu <- struct{}{}
	defer func() { <-l.mu }()
	out := make([]map[string]any, len(l.data))
	copy(out, l.data)
	return out
}

// handleLogs GET /api/logs
func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	data := a.Logs.All()
	writeJSON(w, 200, map[string]any{"success": true, "data": data, "count": len(data), "total": len(data)})
}

// handleSystemLogsDownload GET /api/system/logs/download
func (a *App) handleSystemLogsDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=docker-control-logs.txt")
	for _, l := range a.Logs.All() {
		ts, _ := l["timestamp"].(string)
		lv, _ := l["level"].(string)
		msg, _ := l["message"].(string)
		w.Write([]byte(ts + " [" + lv + "] " + msg + "\n"))
	}
}

// ---------- 模板 ----------

// handleTemplates GET /api/templates
func (a *App) handleTemplates(w http.ResponseWriter, r *http.Request) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	if a.Store.Data.Templates == nil {
		ok(w, []any{})
		return
	}
	ok(w, a.Store.Data.Templates)
}

// handleTemplateGet GET /api/templates/{name}
func (a *App) handleTemplateGet(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/templates/")
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

// handleTemplateSave POST /api/templates/save
func (a *App) handleTemplateSave(w http.ResponseWriter, r *http.Request) {
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
	okMsg(w, "模板已保存")
}

// handleTemplateDelete DELETE /api/templates/{name}
func (a *App) handleTemplateDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/templates/")
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
	okMsg(w, "模板已删除")
}

// ---------- 定时任务 ----------

// handleScheduledTasks GET/POST/PUT/DELETE /api/scheduled-tasks/{container|compose}
func (a *App) handleScheduledTasks(w http.ResponseWriter, r *http.Request) {
	typ := "container"
	if strings.HasSuffix(r.URL.Path, "/compose") {
		typ = "compose"
	}
	switch r.Method {
	case "GET":
		a.Store.mu.RLock()
		tasks := []Task{}
		for _, t := range a.Store.Data.Tasks {
			if t.Type == typ {
				tasks = append(tasks, t)
			}
		}
		a.Store.mu.RUnlock()
		writeJSON(w, 200, map[string]any{"success": true, "tasks": tasks, "data": tasks})
	case "POST":
		m := readBody(r)
		name := bodyStr(m, "container_name")
		if name == "" {
			name = bodyStr(m, "project_name")
		}
		if name == "" {
			fail(w, 400, "缺少任务目标")
			return
		}
		t := Task{
			ID: randHex(8), Type: typ, Name: name,
			Action: bodyStr(m, "action"), Cron: bodyStr(m, "cron_expression"),
			Enabled: true, CreatedAt: a.Store.Now(),
		}
		if t.Cron == "" {
			t.Cron = bodyStr(m, "interval")
		}
		a.Store.mu.Lock()
		a.Store.Data.Tasks = append(a.Store.Data.Tasks, t)
		a.Store.mu.Unlock()
		a.Store.Save()
		ok(w, t)
	case "PUT":
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
				if v, okk := m["enabled"].(bool); okk {
					a.Store.Data.Tasks[i].Enabled = v
				}
			}
		}
		a.Store.mu.Unlock()
		a.Store.Save()
		ok(w, nil)
	case "DELETE":
		id := r.URL.Query().Get("id")
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
}

// handleTaskExecute POST /api/scheduled-tasks/{type}/{id}/execute
func (a *App) handleTaskExecute(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/scheduled-tasks/"), "/")
	if len(parts) < 2 {
		fail(w, 404, "not found")
		return
	}
	id := parts[1]
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
		err = a.startUpdateTask(target.Name)
	default:
		fail(w, 400, "不支持的动作: "+target.Action)
		return
	}
	if err != nil {
		fail(w, 500, "执行失败: "+err.Error())
		return
	}
	okMsg(w, "任务已执行")
}

// ---------- 系统杂项 stub（兼容前端探活类调用） ----------

func (a *App) stubOK(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"success": true, "data": map[string]any{}, "message": "功能已裁剪"})
}

func (a *App) handleIsUnraid(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"success": true, "is_unraid": false, "isUnraid": false})
}

func (a *App) handleSchedulerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"success": true, "running": true, "status": "running",
		"next_run": time.Now().Add(24 * time.Hour).Format("2006-01-02 15:04:05")})
}

func (a *App) handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	a.Logs.Add("WARNING", "🔄 面板重启请求", "system")
	okMsg(w, "重启指令已提交（由容器编排自动拉起）")
	// 异步自杀，交给容器 restart 策略
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
}

// sortStrings 辅助
func sortStrings(s []string) {
	sort.Strings(s)
}
