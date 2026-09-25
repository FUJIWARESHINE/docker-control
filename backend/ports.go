package main

// 端口页：/proc/net/tcp|udp 扫描 + Engine API 端口映射对照
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
	State    string `json:"state"`
}

// handlePortsList GET /api/ports —— 宿主监听（Host 来源）+ 容器映射（Container 来源）
func (a *App) handlePortsList(w http.ResponseWriter, r *http.Request) {
	ports := map[string]*listenPort{}
	for _, f := range []struct {
		path, protocol string
	}{
		{"/proc/net/tcp", "tcp"}, {"/proc/net/tcp6", "tcp"},
		{"/proc/net/udp", "udp"}, {"/proc/net/udp6", "udp"},
	} {
		scanProcNet(f.path, f.protocol, ports)
	}

	cs, _ := a.Docker.ListContainers(true)
	customNames := a.portCustomNames()
	containerRows := []map[string]any{}
	for _, c := range cs {
		name := containerName(c)
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			key := strconv.Itoa(p.PublicPort)
			containerRows = append(containerRows, map[string]any{
				"port":           p.PublicPort,
				"protocol":       p.Type,
				"service":        customNames[key],
				"container":      name,
				"container_port": p.PrivatePort,
				"is_custom":      customNames[key] != "",
			})
			delete(ports, key+"/"+p.Type) // 宿主侧已被容器占用
		}
	}

	rows := []map[string]any{}
	keys := make([]string, 0, len(ports))
	for k := range ports {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := ports[k]
		portStr := strings.Split(k, "/")[0]
		rows = append(rows, map[string]any{
			"port": p.Port, "protocol": p.Protocol, "service": customNames[portStr],
			"container": "", "host_only": true, "is_custom": customNames[portStr] != "",
		})
	}
	rows = append(rows, containerRows...)
	sort.Slice(rows, func(i, j int) bool {
		p1, _ := rows[i]["port"].(int)
		p2, _ := rows[j]["port"].(int)
		return p1 < p2
	})
	ok(w, rows)
}

// scanProcNet 解析 /proc/net/xxx：tcp 取 LISTEN(0A)，udp 全取
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
		if protocol == "tcp" && fields[3] != "0A" {
			continue
		}
		parts := strings.Split(fields[1], ":")
		if len(parts) < 2 {
			continue
		}
		port, err := strconv.ParseInt(parts[len(parts)-1], 16, 32)
		if err != nil || port == 0 {
			continue
		}
		k := strconv.Itoa(int(port)) + "/" + protocol
		if _, exists := ports[k]; !exists {
			ports[k] = &listenPort{Port: int(port), Protocol: protocol, State: "listening"}
		}
	}
}

// handlePortSub 分发已在 routes.go：/api/ports/{port}/name
func (a *App) portSetName(w http.ResponseWriter, r *http.Request, port string) {
	name := bodyStr(readBody(r), "name")
	a.Store.mu.Lock()
	if a.Store.Data.UserPrefs["port_names"] == nil {
		a.Store.Data.UserPrefs["port_names"] = map[string]any{}
	}
	if pm, ok := a.Store.Data.UserPrefs["port_names"].(map[string]any); ok {
		pm[port] = name
	}
	a.Store.mu.Unlock()
	a.Store.Save()
	ok(w, nil)
}

func (a *App) portCustomNames() map[string]string {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	out := map[string]string{}
	if pm, ok := a.Store.Data.UserPrefs["port_names"].(map[string]any); ok {
		for k, v := range pm {
			if str, ok2 := v.(string); ok2 {
				out[k] = str
			}
		}
	}
	return out
}
