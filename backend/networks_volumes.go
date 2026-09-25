package main

// 网络与存储卷 API
import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

// handleNetworksList GET /api/networks
func (a *App) handleNetworksList(w http.ResponseWriter, r *http.Request) {
	nets, err := a.Docker.ListNetworks()
	if err != nil {
		fail(w, 500, "获取网络列表失败: "+err.Error())
		return
	}
	// 容器状态映射
	stateOf := map[string]string{}
	iconOf := map[string]string{}
	if cs, err2 := a.Docker.ListContainers(true); err2 == nil {
		for _, c := range cs {
			name := ""
			if len(c.Names) > 0 {
				name = strings.TrimPrefix(c.Names[0], "/")
			}
			stateOf[c.ID] = c.State
			_, iconOf[c.ID] = a.aliasOf(name)
			_ = name
		}
	}
	data := []map[string]any{}
	for _, n := range nets {
		cons := []map[string]any{}
		for cid, nc := range n.Containers {
			st := stateOf[cid]
			cons = append(cons, map[string]any{
				"name": nc.Name, "status": st, "ipv4": nc.IPv4Address, "icon_path": iconOf[cid],
			})
		}
		sort.Slice(cons, func(i, j int) bool {
			a1, _ := cons[i]["name"].(string)
			b1, _ := cons[j]["name"].(string)
			return a1 < b1
		})
		data = append(data, map[string]any{
			"name": n.Name, "id": n.ID[:12], "driver": n.Driver,
			"created": n.Created, "internal": n.Internal, "containers": cons,
		})
	}
	sort.Slice(data, func(i, j int) bool {
		a1, _ := data[i]["name"].(string)
		b1, _ := data[j]["name"].(string)
		return a1 < b1
	})
	ok(w, data)
}

// handleNetworkDelete DELETE /api/networks/{name}
func (a *App) handleNetworkDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/networks/")
	resp, err := a.Docker.do("DELETE", "/networks/"+name, nil, nil)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e struct{ Message string `json:"message"` }
		json.NewDecoder(resp.Body).Decode(&e)
		fail(w, 500, "删除网络失败: "+e.Message)
		return
	}
	a.Logs.Add("WARNING", "🗑 删除网络 ["+name+"]", "realtime")
	ok(w, nil)
}

// handleNetworksPrune POST /api/networks/prune
func (a *App) handleNetworksPrune(w http.ResponseWriter, r *http.Request) {
	var out map[string]any
	if err := a.Docker.doJSON("POST", "/networks/prune", nil, map[string]any{}, &out); err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.Logs.Add("SUCCESS", "🧹 未使用网络清理完成", "realtime")
	ok(w, out)
}

// handleVolumesList GET /api/volumes
func (a *App) handleVolumesList(w http.ResponseWriter, r *http.Request) {
	var out struct {
		Volumes []struct {
			Name       string            `json:"Name"`
			Driver     string            `json:"Driver"`
			Mountpoint string            `json:"Mountpoint"`
			CreatedAt  string            `json:"CreatedAt"`
			Labels     map[string]string `json:"Labels"`
		} `json:"Volumes"`
	}
	if err := a.Docker.doJSON("GET", "/volumes", nil, nil, &out); err != nil {
		fail(w, 500, "获取存储卷列表失败: "+err.Error())
		return
	}
	// 使用情况：从容器 Mounts 统计
	useCount := map[string][]map[string]any{}
	if cs, err := a.Docker.ListContainers(true); err == nil {
		for _, c := range cs {
			name := ""
			if len(c.Names) > 0 {
				name = strings.TrimPrefix(c.Names[0], "/")
			}
			_, icon := a.aliasOf(name)
			for _, m := range c.Mounts {
				if m.Type == "volume" && m.Name != "" {
					useCount[m.Name] = append(useCount[m.Name], map[string]any{"name": name, "icon_path": icon})
				}
			}
		}
	}
	data := []map[string]any{}
	for _, v := range out.Volumes {
		cons := useCount[v.Name]
		if cons == nil {
			cons = []map[string]any{}
		}
		data = append(data, map[string]any{
			"name":       v.Name,
			"mountpoint": v.Mountpoint,
			"driver":     v.Driver,
			"created":    v.CreatedAt,
			"usage": map[string]any{
				"ref_count":  len(cons),
				"containers": cons,
			},
		})
	}
	sort.Slice(data, func(i, j int) bool {
		a1, _ := data[i]["name"].(string)
		b1, _ := data[j]["name"].(string)
		return a1 < b1
	})
	ok(w, data)
}

// handleVolumeDelete DELETE /api/volumes/{name}
func (a *App) handleVolumeDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/volumes/")
	resp, err := a.Docker.do("DELETE", "/volumes/"+name, nil, nil)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e struct{ Message string `json:"message"` }
		json.NewDecoder(resp.Body).Decode(&e)
		fail(w, 500, "删除存储卷失败: "+e.Message)
		return
	}
	a.Logs.Add("WARNING", "🗑 删除存储卷 ["+name+"]", "realtime")
	ok(w, nil)
}

// handleVolumesPrune POST /api/volumes/prune
func (a *App) handleVolumesPrune(w http.ResponseWriter, r *http.Request) {
	var out map[string]any
	if err := a.Docker.doJSON("POST", "/volumes/prune", nil, map[string]any{}, &out); err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.Logs.Add("SUCCESS", "🧹 未使用存储卷清理完成", "realtime")
	ok(w, out)
}

var _ = time.Now
