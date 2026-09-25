// Package main — Docker Control 自研后端
// 纯 Docker 管理面板后端：Gin-free，仅用标准库 + gorilla/websocket。
// 通过 /var/run/docker.sock 调用 Docker Engine API，不依赖 diancup 二进制。
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ---------- 全局状态 ----------

type App struct {
	Store    *Store
	Docker   *DockerClient
	Hub      *Hub // websocket 广播中心
	Logs     *LogRing
	Tasks    *TaskManager // 后台任务（更新容器等）
	Version  string
	SelfName string // 自身容器名（diancup.self 兼容标记过滤用）
}

var app *App

func main() {
	port := flag.String("port", envOr("PORT", "9527"), "监听端口")
	dataDir := flag.String("data", envOr("DATA_DIR", "./data"), "数据目录")
	staticDir := flag.String("static", envOr("STATIC_DIR", "./static"), "前端静态目录")
	flag.Parse()

	os.MkdirAll(*dataDir, 0755)

	store := NewStore(filepath.Join(*dataDir, "config.json"))
	dc := NewDockerClient(envOr("DOCKER_HOST", "unix:///var/run/docker.sock"))
	hub := NewHub()
	go hub.Run()

	a := &App{
		Store:   store,
		Docker:  dc,
		Hub:     hub,
		Logs:    NewLogRing(500),
		Tasks:   NewTaskManager(),
		Version: "0.5.0",
		SelfName: envOr("SELF_CONTAINER", "docker-control"),
	}
	app = a
	a.Logs.Add("INFO", "🚀 Docker Control 后端启动 v"+a.Version, "system")

	// 自动更新调度器
	go autoUpdateLoop()

	mux := http.NewServeMux()
	registerRoutes(mux)

	// 静态文件（前端）
	fs := http.FileServer(http.Dir(*staticDir))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" || p == "/index.html" {
			if !a.publicAuthOk(r) {
				http.Redirect(w, r, "/static/login.html", http.StatusFound)
				return
			}
			http.ServeFile(w, r, filepath.Join(*staticDir, "index.html"))
			return
		}
		if p == "/login" || p == "/login.html" {
			http.ServeFile(w, r, filepath.Join(*staticDir, "login.html"))
			return
		}
		// 其余根路径文件（favicon 等）回退到 static
		http.ServeFile(w, r, filepath.Join(*staticDir, strings.TrimPrefix(p, "/")))
	})

	log.Printf("Docker Control v%s 监听 :%s (data=%s static=%s)", a.Version, *port, *dataDir, *staticDir)
	if err := http.ListenAndServe(":"+*port, mux); err != nil {
		log.Fatal(err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---------- 通用 JSON 响应 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, data any) {
	if data == nil {
		writeJSON(w, 200, map[string]any{"success": true})
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "data": data})
}

func okMsg(w http.ResponseWriter, msg string) {
	writeJSON(w, 200, map[string]any{"success": true, "message": msg})
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"success": false, "message": msg})
}

// readBody 解析 JSON 请求体到 map
func readBody(r *http.Request) map[string]any {
	var m map[string]any
	json.NewDecoder(io_Limit(r)).Decode(&m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func bodyStr(m map[string]any, k string) string {
	if v, okk := m[k].(string); okk {
		return v
	}
	return ""
}

func bodyBool(m map[string]any, k string) bool {
	v, okk := m[k].(bool)
	return okk && v
}

// ---------- 会话与中间件 ----------

var tokens struct {
	sync.RWMutex
	m map[string]int64 // token -> 创建时间
}

func init() {
	tokens.m = map[string]int64{}
}

// authMiddleware：除 public 白名单外全部要求 Bearer token 或 X-API-Key
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if app.publicAuthOk(r) {
			next(w, r)
			return
		}
		fail(w, 401, "未授权")
	}
}

// publicAuthOk 校验 token/apikey/cookie；public 端点自行处理不经过此函数
func (a *App) publicAuthOk(r *http.Request) bool {
	// 1. Authorization Bearer（authenticatedFetch 场景）
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		tk := strings.TrimPrefix(auth, "Bearer ")
		tokens.RLock()
		_, okk := tokens.m[tk]
		tokens.RUnlock()
		if okk {
			return true
		}
	}
	// 2. Cookie 会话（裸 fetch 场景：stats/images/networks/volumes/ports 等 loader）
	if ck, err := r.Cookie("dc_token"); err == nil && ck.Value != "" {
		tokens.RLock()
		_, okk := tokens.m[ck.Value]
		tokens.RUnlock()
		if okk {
			return true
		}
	}
	// 3. API Key
	if k := r.Header.Get("X-API-Key"); k != "" && a.Store.HasAPIKey(k) {
		return true
	}
	// 4. websocket / 浏览器直连场景允许 ?token= 参数
	if tk := r.URL.Query().Get("token"); tk != "" {
		tokens.RLock()
		_, okk := tokens.m[tk]
		tokens.RUnlock()
		if okk {
			return true
		}
	}
	return false
}
