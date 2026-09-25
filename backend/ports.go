package main

// 端口扫描：解析 /proc/net/tcp{,6} /udp{,6} + Docker 端口映射
import (
	"bufio"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

type listenPort struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Service  string `json:"service"`
	Process  string `json:"process"`
	State    string `json:"state"`
}

// handlePortsList GET /api/ports
func (a *App) handlePortsList(w http.ResponseWriter, r *http.Request) {
	ports := map[string]*listenPort{} // "80/tcp" -> port

	// 1. 宿主机监听端口（host 网络模式下即容器网络栈）
	for _, f := range []struct {
		path     string
		protocol string
	}{
		{"/proc/net/tcp", "tcp"}, {"/proc/net/tcp6", "tcp"},
		{"/proc/net/udp", "udp"}, {"/proc/net/udp6", "udp"},
	} {
		scanProcNet(f.path, f.protocol, ports)
	}

	// 2. Docker 容器端口映射（Container 来源行）
	cs, _ := a.Docker.ListContainers(true)
	containerRows := []map[string]any{}
	customNames := a.portCustomNames()
	for _, c := range cs {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		_, icon := a.aliasOf(name)
		_ = icon
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			containerRows = append(containerRows, map[string]any{
				"port":           p.PublicPort,
				"protocol":       p.Type,
				"service":        customNames[strconv.Itoa(p.PublicPort)],
				"container":      name,
				"container_port": p.PrivatePort,
				"is_custom":      customNames[strconv.Itoa(p.PublicPort)] != "",
			})
			// 宿主侧已被容器占用
			k := strconv.Itoa(p.PublicPort) + "/" + p.Type
			delete(ports, k)
		}
	}

	rows := []map[string]any{}
	// 宿主监听行（Host 来源）
	keys := make([]string, 0, len(ports))
	for k := range ports {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := ports[k]
		portStr := strings.Split(k, "/")[0]
		rows = append(rows, map[string]any{
			"port":      p.Port,
			"protocol":  p.Protocol,
			"service":   customNames[portStr],
			"container": "",
			"host_only": true,
			"is_custom": customNames[portStr] != "",
		})
	}
	rows = append(rows, containerRows...)
	// 端口排序
	sort.Slice(rows, func(i, j int) bool {
		p1, _ := rows[i]["port"].(int)
		p2, _ := rows[j]["port"].(int)
		return p1 < p2
	})
	ok(w, rows)
}

// scanProcNet 解析 /proc/net/xxx 中 state=0A(tcp listen) 或 udp 的端口
func scanProcNet(path, protocol string, ports map[string]*listenPort) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // 跳过表头
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		local := fields[1]
		state := ""
		if len(fields) > 3 {
			state = fields[3]
		}
		if protocol == "tcp" && state != "0A" { // 0A = LISTEN
			continue
		}
		parts := strings.Split(local, ":")
		if len(parts) < 2 {
			continue
		}
		portHex := parts[len(parts)-1]
		port, err := strconv.ParseInt(portHex, 16, 32)
		if err != nil || port == 0 {
			continue
		}
		k := strconv.Itoa(int(port)) + "/" + protocol
		if _, exists := ports[k]; !exists {
			ports[k] = &listenPort{Port: int(port), Protocol: protocol, State: "listening"}
		}
	}
}

// handlePortCustomName POST /api/ports/{port}/custom-name
func (a *App) handlePortCustomName(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/ports/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[1] != "custom-name" {
		fail(w, 404, "not found")
		return
	}
	port := parts[0]
	m := readBody(r)
	name := bodyStr(m, "name")
	if name == "" {
		name = bodyStr(m, "custom_name")
	}
	a.Store.mu.Lock()
	if a.Store.Data.UserPrefs["port_names"] == nil {
		a.Store.Data.UserPrefs["port_names"] = map[string]any{}
	}
	if pm, okk := a.Store.Data.UserPrefs["port_names"].(map[string]any); okk {
		pm[port] = name
	}
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, nil)
}

// portCustomNames 读取端口备注
func (a *App) portCustomNames() map[string]string {
	s := a.Store
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	if pm, okk := s.Data.UserPrefs["port_names"].(map[string]any); okk {
		for k, v := range pm {
			if str, ok2 := v.(string); ok2 {
				out[k] = str
			}
		}
	}
	return out
}
