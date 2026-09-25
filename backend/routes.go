package main

// 路由注册 + 杂项辅助
import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// io_Limit 限制请求体 64MB
func io_Limit(r *http.Request) io.Reader {
	return io.LimitReader(r.Body, 64<<20)
}

func registerRoutes(mux *http.ServeMux) {
	// Public（免认证）
	mux.HandleFunc("/api/auth/login", app.handleAuthLogin)
	mux.HandleFunc("/api/public/theme", app.handlePublicTheme)
	mux.HandleFunc("/api/public/password-status", app.handlePublicPasswordStatus)
	mux.HandleFunc("/api/public/version", app.handlePublicVersion)
	mux.HandleFunc("/api/disclaimer/status", app.handleDisclaimerStatus)
	mux.HandleFunc("/api/disclaimer/agree", app.handleDisclaimerAgree)
	mux.HandleFunc("/api/disclaimer/agree-forever", app.handleDisclaimerAgree)
	mux.HandleFunc("/api/disclaimer/disagree", app.handleDisclaimerAgree)

	// WebSocket
	mux.HandleFunc("/ws", app.handleWS)

	// 认证
	mux.HandleFunc("/api/auth/logout", authMiddleware(app.handleAuthLogout))
	mux.HandleFunc("/api/logout", authMiddleware(app.handleAuthLogout))
	mux.HandleFunc("/api/auth/change-password", authMiddleware(app.handleChangePassword))
	mux.HandleFunc("/api/password", authMiddleware(app.handleChangePassword))

	// 容器
	mux.HandleFunc("/api/containers/stats/batch", authMiddleware(app.handleStatsBatch))
	mux.HandleFunc("/api/containers/create", authMiddleware(app.handleContainerCreate))
	mux.HandleFunc("/api/containers/", authMiddleware(app.handleContainerInspect)) // {id}/inspect
	mux.HandleFunc("/api/containers", authMiddleware(app.handleContainersList))
	mux.HandleFunc("/api/container/start", authMiddleware(app.handleContainerAction))
	mux.HandleFunc("/api/container/stop", authMiddleware(app.handleContainerAction))
	mux.HandleFunc("/api/container/restart", authMiddleware(app.handleContainerAction))
	mux.HandleFunc("/api/container/kill", authMiddleware(app.handleContainerAction))
	mux.HandleFunc("/api/container/pause", authMiddleware(app.handleContainerAction))
	mux.HandleFunc("/api/container/unpause", authMiddleware(app.handleContainerAction))
	mux.HandleFunc("/api/container/delete", authMiddleware(app.handleContainerDelete))
	mux.HandleFunc("/api/container/generate-compose", authMiddleware(app.handleGenerateCompose))
	mux.HandleFunc("/api/generate-compose", authMiddleware(app.handleGenerateCompose))
	mux.HandleFunc("/api/container/logs", authMiddleware(app.handleContainerLogs))
	mux.HandleFunc("/api/container/icon/", authMiddleware(app.handleIconServe))
	mux.HandleFunc("/api/container/icon", authMiddleware(app.handleIconServe))

	// 更新
	mux.HandleFunc("/api/check-updates", authMiddleware(app.handleCheckUpdates))
	mux.HandleFunc("/api/update-container", authMiddleware(app.handleUpdateContainer))
	mux.HandleFunc("/api/toggle-auto-update", authMiddleware(app.handleToggleAutoUpdate))

	// 镜像
	mux.HandleFunc("/api/images/pull", authMiddleware(app.handleImagePull))
	mux.HandleFunc("/api/images/prune-dangling", authMiddleware(app.handleImagesPrune))
	mux.HandleFunc("/api/images/prune", authMiddleware(app.handleImagesPrune))
	mux.HandleFunc("/api/images/export", authMiddleware(app.handleImageExport))
	mux.HandleFunc("/api/images/import", authMiddleware(app.handleImageImport))
	mux.HandleFunc("/api/images/upload", authMiddleware(app.handleImageUpload))
	mux.HandleFunc("/api/images/", authMiddleware(app.handleImageSub)) // {id}/tag, {id} delete
	mux.HandleFunc("/api/images", authMiddleware(app.handleImagesList))

	// 网络 / 卷
	mux.HandleFunc("/api/networks/prune", authMiddleware(app.handleNetworksPrune))
	mux.HandleFunc("/api/networks/", authMiddleware(app.handleNetworkDelete))
	mux.HandleFunc("/api/networks", authMiddleware(app.handleNetworksList))
	mux.HandleFunc("/api/volumes/prune", authMiddleware(app.handleVolumesPrune))
	mux.HandleFunc("/api/volumes/", authMiddleware(app.handleVolumeDelete))
	mux.HandleFunc("/api/volumes", authMiddleware(app.handleVolumesList))

	// 端口
	mux.HandleFunc("/api/ports/", authMiddleware(app.handlePortCustomName))
	mux.HandleFunc("/api/ports", authMiddleware(app.handlePortsList))

	// Compose
	mux.HandleFunc("/api/compose/projects", authMiddleware(app.handleComposeProjects))
	mux.HandleFunc("/api/compose/save", authMiddleware(app.handleComposeSave))
	mux.HandleFunc("/api/compose/file", authMiddleware(app.handleComposeFile))
	mux.HandleFunc("/api/compose/logs", authMiddleware(app.handleComposeLogs))
	mux.HandleFunc("/api/compose/build", authMiddleware(app.handleComposeAction))
	mux.HandleFunc("/api/compose/", authMiddleware(app.handleComposeAction))

	// 模板
	mux.HandleFunc("/api/templates/save", authMiddleware(app.handleTemplateSave))
	mux.HandleFunc("/api/templates/", authMiddleware(app.handleTemplateSub))
	mux.HandleFunc("/api/templates", authMiddleware(app.handleTemplates))

	// 定时任务
	mux.HandleFunc("/api/scheduled-tasks/container", authMiddleware(app.handleScheduledTasks))
	mux.HandleFunc("/api/scheduled-tasks/compose", authMiddleware(app.handleScheduledTasks))
	mux.HandleFunc("/api/scheduled-tasks/", authMiddleware(app.handleTaskExecute))

	// 配置 / 偏好 / 别名 / APIKey
	mux.HandleFunc("/api/config/aliases", authMiddleware(app.handleAliases))
	mux.HandleFunc("/api/config/backup", authMiddleware(app.handleConfigBackup))
	mux.HandleFunc("/api/config/restore", authMiddleware(app.handleConfigRestore))
	mux.HandleFunc("/api/config", authMiddleware(app.handleConfig))
	mux.HandleFunc("/api/user-prefs/get", authMiddleware(app.handleUserPrefsGet))
	mux.HandleFunc("/api/user-prefs/set", authMiddleware(app.handleUserPrefsSet))
	mux.HandleFunc("/api/apikey/list", authMiddleware(app.handleAPIKeyList))
	mux.HandleFunc("/api/apikey/generate", authMiddleware(app.handleAPIKeyGenerate))
	mux.HandleFunc("/api/apikey/toggle", authMiddleware(app.handleAPIKeyToggle))
	mux.HandleFunc("/api/apikey/delete", authMiddleware(app.handleAPIKeyDelete))

	// 日志 / 系统
	mux.HandleFunc("/api/logs", authMiddleware(app.handleLogs))
	mux.HandleFunc("/api/system/logs/download", authMiddleware(app.handleSystemLogsDownload))
	mux.HandleFunc("/api/system/restart", authMiddleware(app.handleSystemRestart))
	mux.HandleFunc("/api/restart", authMiddleware(app.handleSystemRestart))
	mux.HandleFunc("/api/version", authMiddleware(app.handlePublicVersion))
	mux.HandleFunc("/api/scheduler/status", authMiddleware(app.handleSchedulerStatus))
	mux.HandleFunc("/api/host/is-unraid", authMiddleware(app.handleIsUnraid))
	mux.HandleFunc("/api/background-tasks", authMiddleware(app.handleBackgroundTasks))
	mux.HandleFunc("/api/backup", authMiddleware(app.handleConfigBackup))

	// 已裁剪功能的 stub（避免前端报错）
	for _, p := range []string{
		"/api/host", "/api/host/cpu", "/api/host/monitor", "/api/host/config",
		"/api/ssh", "/api/sftp", "/api/files", "/api/filesystem", "/api/probe",
		"/api/telegram", "/api/wechat", "/api/apppush", "/api/notes", "/api/weather",
		"/api/calendar", "/api/nav", "/api/navigation", "/api/icons", "/api/registry",
		"/api/self-update", "/api/quick-deploy", "/api/proxy-config", "/api/settings",
	} {
		mux.HandleFunc(p, authMiddleware(app.stubOK))
		mux.HandleFunc(p+"/", authMiddleware(app.stubOK))
	}
}

// handleConfig GET/POST /api/config 统一入口
func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.handleGetConfig(w, r)
		return
	}
	a.handleSaveConfig(w, r)
}

// handleImageSub 分发 /api/images/{id} 与 /api/images/{id}/tag
func (a *App) handleImageSub(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/images/")
	if strings.HasSuffix(p, "/tag") && r.Method == "POST" {
		a.handleImageTag(w, r)
		return
	}
	if r.Method == "DELETE" || r.Method == "POST" {
		a.handleImageDelete(w, r)
		return
	}
	fail(w, 404, "not found")
}

// handleTemplateSub 分发 /api/templates/{name}
func (a *App) handleTemplateSub(w http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		a.handleTemplateDelete(w, r)
		return
	}
	a.handleTemplateGet(w, r)
}

// handleBackgroundTasks GET /api/background-tasks
func (a *App) handleBackgroundTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"success": true, "data": []any{}, "tasks": []any{}})
}

// handleIconServe GET /api/container/icon/{name} — 容器图标（从数据目录 icons/ 读取）
func (a *App) handleIconServe(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/container/icon/")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, a.Store.dataDir()+"/icons/"+name)
}

// handleConfigBackup GET /api/config/backup
func (a *App) handleConfigBackup(w http.ResponseWriter, r *http.Request) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=docker-control-backup.json")
	json.NewEncoder(w).Encode(a.Store.Data)
}

// handleConfigRestore POST /api/config/restore
func (a *App) handleConfigRestore(w http.ResponseWriter, r *http.Request) {
	fail(w, 400, "恢复备份请手动替换 data/config.json 后重启容器")
}
