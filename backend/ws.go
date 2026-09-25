package main

// WebSocket 广播中心（日志 / 容器更新进度 / 容器列表变更）
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

func NewHub() *Hub {
	return &Hub{clients: map[*websocket.Conn]bool{}}
}

func (h *Hub) Run() {}

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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleWS GET /ws
func (a *App) handleWS(w http.ResponseWriter, r *http.Request) {
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
	// 读循环：处理 ping/pong 保活
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
