package main

// WebSocket 广播中心（日志 / 拉取与更新进度 / 容器列表变更）
import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Hub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]bool
}

func NewHub() *Hub { return &Hub{clients: map[*websocket.Conn]bool{}} }

func (h *Hub) add(c *websocket.Conn) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

func (h *Hub) remove(c *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Broadcast 向所有连接推送 {type, data}
func (h *Hub) Broadcast(typ string, data any) {
	msg, _ := json.Marshal(map[string]any{"type": typ, "data": data})
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.WriteMessage(websocket.TextMessage, msg)
	}
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// handleWS GET /ws?token=xxx —— 连接时校验会话 token（v1 补强：旧版无鉴权）
func (a *App) handleWS(w http.ResponseWriter, r *http.Request) {
	if !a.authed(r) {
		fail(w, 401, "未授权")
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("ws upgrade:", err)
		return
	}
	a.Hub.add(conn)
	defer func() {
		a.Hub.remove(conn)
		conn.Close()
	}()
	conn.SetReadDeadline(time.Time{})
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage {
			var m struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(msg, &m) == nil && m.Type == "ping" {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"pong","data":{}}`))
			}
		}
	}
}

// ---------- 操作日志环形缓冲 ----------

type LogRing struct {
	mu   chan struct{}
	data []map[string]any
	max  int
}

func NewLogRing(max int) *LogRing {
	return &LogRing{mu: make(chan struct{}, 1), data: []map[string]any{}, max: max}
}

func (l *LogRing) Add(level, message, typ string) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	l.mu <- struct{}{}
	l.data = append(l.data, map[string]any{
		"timestamp": ts, "level": level, "message": message, "type": typ,
	})
	if len(l.data) > l.max {
		l.data = l.data[len(l.data)-l.max:]
	}
	<-l.mu
	app.Hub.Broadcast("log", map[string]any{
		"level": level, "message": message, "type": typ, "timestamp": ts,
	})
}

func (l *LogRing) All() []map[string]any {
	l.mu <- struct{}{}
	defer func() { <-l.mu }()
	out := make([]map[string]any, len(l.data))
	copy(out, l.data)
	return out
}
