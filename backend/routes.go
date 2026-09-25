package main

// 路由注册。统一 REST 风格：/api/资源/{id}/{动作}
import (
	"net/http"
	"strings"
)

func registerRoutes(mux *http.ServeMux) {
	// 免认证
	mux.HandleFunc("/api/auth/login", app.handleLogin)
	mux.HandleFunc("/api/auth/status", app.handleAuthStatus)

	// WebSocket（连接后校验 token）
	mux.HandleFunc("/ws", app.handleWS)

	// 认证
	mux.HandleFunc("/api/auth/logout", app.guard(app.handleLogout))
	mux.HandleFunc("/api/auth/password", app.guard(app.handleChangePassword))

	// 容器
	mux.HandleFunc("/api/containers", app.guard(app.handleContainersList))
	mux.HandleFunc("/api/containers/", app.guard(app.handleContainerSub)) // {id}/{action}
	mux.HandleFunc("/api/stats", app.guard(app.handleStatsBatch))

	// 镜像
	mux.HandleFunc("/api/images", app.guard(app.handleImagesList))
	mux.HandleFunc("/api/images/", app.guard(app.handleImageSub))
	mux.HandleFunc("/api/images/pull", app.guard(app.handleImagePull)) // 上行匹配需在 /api/images/ 之前？见 handleImageSub 分流

	// 网络 / 卷
	mux.HandleFunc("/api/networks", app.guard(app.handleNetworksList))
	mux.HandleFunc("/api/networks/", app.guard(app.handleNetworkSub))
	mux.HandleFunc("/api/volumes", app.guard(app.handleVolumesList))
	mux.HandleFunc("/api/volumes/", app.guard(app.handleVolumeSub))

	// 端口
	mux.HandleFunc("/api/ports", app.guard(app.handlePortsList))
	mux.HandleFunc("/api/ports/", app.guard(app.handlePortSub))

	// Compose
	mux.HandleFunc("/api/compose", app.guard(app.handleComposeProjects))
	mux.HandleFunc("/api/compose/", app.guard(app.handleComposeSub))

	// 更新
	mux.HandleFunc("/api/updates/check", app.guard(app.handleCheckUpdates))
	mux.HandleFunc("/api/updates/settings", app.guard(app.handleUpdateSettings))
	mux.HandleFunc("/api/updates/", app.guard(app.handleUpdateSub)) // {name}/apply | {name}/auto

	// 模板 / 定时任务
	mux.HandleFunc("/api/templates", app.guard(app.handleTemplates))
	mux.HandleFunc("/api/templates/", app.guard(app.handleTemplateSub))
	mux.HandleFunc("/api/tasks", app.guard(app.handleTasks))
	mux.HandleFunc("/api/tasks/", app.guard(app.handleTaskSub)) // {id}/execute

	// 配置 / 别名 / 偏好 / API Key
	mux.HandleFunc("/api/config", app.guard(app.handleConfig))
	mux.HandleFunc("/api/aliases", app.guard(app.handleAliases))
	mux.HandleFunc("/api/prefs", app.guard(app.handlePrefs))
	mux.HandleFunc("/api/apikeys", app.guard(app.handleAPIKeys))
	mux.HandleFunc("/api/apikeys/", app.guard(app.handleAPIKeySub)) // {id}/toggle

	// 日志 / 系统 / 备份
	mux.HandleFunc("/api/logs", app.guard(app.handleLogs))
	mux.HandleFunc("/api/logs/download", app.guard(app.handleLogsDownload))
	mux.HandleFunc("/api/system/info", app.guard(app.handleSystemInfo))
	mux.HandleFunc("/api/system/restart", app.guard(app.handleSystemRestart))
	mux.HandleFunc("/api/backup", app.guard(app.handleBackup))
}

// guard 认证中间件
func (a *App) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authed(r) {
			fail(w, 401, "未授权")
			return
		}
		next(w, r)
	}
}

// ---------- 容器子路由分发 ----------

// handleContainerSub 分发 /api/containers/{id}/{action}
// 动作: start stop restart kill pause unpause delete inspect logs compose
func (a *App) handleContainerSub(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/containers/")
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		fail(w, 404, "not found")
		return
	}
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "start", "stop", "restart", "kill", "pause", "unpause":
		a.containerAction(w, r, id, action)
	case "delete", "":
		if r.Method == http.MethodDelete || action == "delete" {
			a.containerDelete(w, r, id)
			return
		}
		fail(w, 405, "method not allowed")
	case "inspect":
		a.containerInspect(w, r, id)
	case "logs":
		a.containerLogs(w, r, id)
	case "compose":
		a.containerGenerateCompose(w, r, id)
	default:
		fail(w, 404, "未知操作: "+action)
	}
}

// ---------- 镜像子路由分发 ----------

// handleImageSub 分发 /api/images/{id}、/api/images/{id}/tag、/api/images/prune、/api/images/load
// 注意：/api/images/pull 已被精确路由截获，不会进入这里。
func (a *App) handleImageSub(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/images/"), "/")
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		fail(w, 404, "not found")
		return
	}
	switch parts[0] {
	case "prune":
		a.imagesPrune(w, r)
	case "load":
		a.imageLoad(w, r)
	default:
		id := parts[0]
		if len(parts) > 1 && parts[1] == "tag" {
			a.imageTag(w, r, id)
			return
		}
		if r.Method == http.MethodDelete {
			a.imageDelete(w, r, id)
			return
		}
		fail(w, 405, "method not allowed")
	}
}

// ---------- 网络 / 卷子路由 ----------

func (a *App) handleNetworkSub(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/networks/"), "/")
	if tail == "prune" {
		a.networksPrune(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		a.networkDelete(w, r, tail)
		return
	}
	fail(w, 405, "method not allowed")
}

func (a *App) handleVolumeSub(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/volumes/"), "/")
	if tail == "prune" {
		a.volumesPrune(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		a.volumeDelete(w, r, tail)
		return
	}
	fail(w, 405, "method not allowed")
}

// ---------- 端口子路由 ----------

func (a *App) handlePortSub(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/ports/"), "/")
	parts := strings.Split(tail, "/")
	// /api/ports/{port}/name
	if len(parts) >= 2 && parts[1] == "name" {
		a.portSetName(w, r, parts[0])
		return
	}
	fail(w, 404, "not found")
}

// ---------- Compose 子路由 ----------

func (a *App) handleComposeSub(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/compose/"), "/")
	parts := strings.Split(tail, "/")
	switch parts[0] {
	case "file":
		a.composeFile(w, r)
	case "logs":
		a.composeLogs(w, r)
	default:
		// /api/compose/{project}/{action} 或 /api/compose/{project}
		project := parts[0]
		action := "status"
		if len(parts) > 1 {
			action = parts[1]
		}
		a.composeAction(w, r, project, action)
	}
}

// ---------- 更新子路由 ----------

func (a *App) handleUpdateSub(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/updates/"), "/")
	parts := strings.Split(tail, "/")
	if len(parts) < 2 {
		fail(w, 404, "not found")
		return
	}
	name := parts[0]
	switch parts[1] {
	case "apply":
		a.updateApply(w, r, name)
	case "auto":
		a.updateAutoToggle(w, r, name)
	default:
		fail(w, 404, "not found")
	}
}

// ---------- 模板 / 任务子路由 ----------

func (a *App) handleTemplateSub(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/templates/"), "/")
	if r.Method == http.MethodDelete {
		a.templateDelete(w, r, name)
		return
	}
	a.templateGet(w, r, name)
}

func (a *App) handleTaskSub(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
	parts := strings.Split(tail, "/")
	id := parts[0]
	if len(parts) > 1 && parts[1] == "execute" {
		a.taskExecute(w, r, id)
		return
	}
	if r.Method == http.MethodDelete {
		a.taskDelete(w, r, id)
		return
	}
	fail(w, 405, "method not allowed")
}

// ---------- API Key 子路由 ----------

func (a *App) handleAPIKeySub(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/apikeys/"), "/")
	parts := strings.Split(tail, "/")
	if len(parts) >= 2 && parts[1] == "toggle" {
		a.apikeyToggle(w, r, parts[0])
		return
	}
	if r.Method == http.MethodDelete {
		a.apikeyDelete(w, r, parts[0])
		return
	}
	fail(w, 405, "method not allowed")
}
