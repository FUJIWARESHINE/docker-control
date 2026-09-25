// Docker Control v1.0 — 纯 Docker 管理面板后端
// 标准库 + gorilla/websocket，直连 Docker Engine API，零面板依赖。
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const Version = "1.0.0"

// App 全局运行时
type App struct {
	Store    *Store
	Docker   *DockerClient
	Hub      *Hub
	Logs     *LogRing
	Tasks    *TaskManager
	SelfName string // 自身容器名（自保护）
}

var app *App

func main() {
	port := flag.String("port", envOr("PORT", "9527"), "监听端口")
	dataDir := flag.String("data", envOr("DATA_DIR", "./data"), "数据目录")
	staticDir := flag.String("static", envOr("STATIC_DIR", "./static"), "前端目录")
	flag.Parse()

	os.MkdirAll(*dataDir, 0755)

	a := &App{
		Store:    NewStore(filepath.Join(*dataDir, "config.json")),
		Docker:   NewDockerClient(envOr("DOCKER_HOST", "unix:///var/run/docker.sock")),
		Hub:      NewHub(),
		Logs:     NewLogRing(500),
		Tasks:    NewTaskManager(),
		SelfName: envOr("SELF_CONTAINER", "docker-control"),
	}
	app = a
	a.Logs.Add("INFO", "Docker Control v"+Version+" 已启动", "system")

	go autoUpdateLoop()

	mux := http.NewServeMux()
	registerRoutes(mux)

	// 前端静态资源
	fs := http.FileServer(http.Dir(*staticDir))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch p {
		case "/", "/index.html":
			if !a.authed(r) {
				http.Redirect(w, r, "/static/login.html", http.StatusFound)
				return
			}
			http.ServeFile(w, r, filepath.Join(*staticDir, "index.html"))
		case "/login", "/login.html":
			http.ServeFile(w, r, filepath.Join(*staticDir, "login.html"))
		default:
			http.ServeFile(w, r, filepath.Join(*staticDir, strings.TrimPrefix(p, "/")))
		}
	})

	log.Printf("Docker Control v%s 监听 :%s (data=%s static=%s)", Version, *port, *dataDir, *staticDir)
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

// ---------- JSON 响应与请求体辅助 ----------

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

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"success": false, "message": msg})
}

func readBody(r *http.Request) map[string]any {
	var m map[string]any
	json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func bodyStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func bodyBool(m map[string]any, k string) bool {
	v, ok := m[k].(bool)
	return ok && v
}

// pathTail 取路径前缀后的剩余部分，如 /api/images/abc/tag → abc/tag
func pathTail(p, prefix string) string {
	return strings.Trim(p, "/")[len(strings.Trim(prefix, "/"))+0:]
}
