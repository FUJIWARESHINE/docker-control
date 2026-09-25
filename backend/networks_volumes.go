package main

// 网络与存储卷管理
import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// handleNetworksList GET /api/networks
func (a *App) handleNetworksList(w http.ResponseWriter, r *http.Request) {
	nets, err := a.Docker.ListNetworks()
	if err != nil {
		fail(w, 500, "获取网络列表失败: "+err.Error())
		return
	}
	stateOf := map[string]string{}
	if cs, err2 := a.Docker.ListContainers(true); err2 == nil {
		for _, c := range cs {
			stateOf[c.ID] = c.State
		}
	}
	data := []map[string]any{}
	for _, n := range nets {
		cons := []map[string]any{}
		for cid, nc := range n.Containers {
			cons = append(cons, map[string]any{
				"name": nc.Name, "status": stateOf[cid], "ipv4": nc.IPv4Address,
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

func dockerErrMessage(resp *http.Response) string {
	var e struct {
		Message string `json:"message"`
	}
	json.NewDecoder(resp.Body).Decode(&e)
	return e.Message
}

func (a *App) networkDelete(w http.ResponseWriter, r *http.Request, name string) {
	resp, err := a.Docker.do("DELETE", "/networks/"+name, nil, nil)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		fail(w, 500, "删除网络失败: "+dockerErrMessage(resp))
		return
	}
	a.Logs.Add("WARNING", "删除网络 ["+name+"]", "realtime")
	ok(w, nil)
}

func (a *App) networksPrune(w http.ResponseWriter, r *http.Request) {
	var out map[string]any
	if err := a.Docker.doJSON("POST", "/networks/prune", nil, map[string]any{}, &out); err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.Logs.Add("SUCCESS", "未使用网络清理完成", "realtime")
	ok(w, out)
}

// handleVolumesList GET /api/volumes
func (a *App) handleVolumesList(w http.ResponseWriter, r *http.Request) {
	var out struct {
		Volumes []struct {
			Name       string `json:"Name"`
			Driver     string `json:"Driver"`
			Mountpoint string `json:"Mountpoint"`
			CreatedAt  string `json:"CreatedAt"`
		} `json:"Volumes"`
	}
	if err := a.Docker.doJSON("GET", "/volumes", nil, nil, &out); err != nil {
		fail(w, 500, "获取存储卷列表失败: "+err.Error())
		return
	}
	useCount := map[string][]string{}
	if cs, err := a.Docker.ListContainers(true); err == nil {
		for _, c := range cs {
			for _, m := range c.Mounts {
				if m.Type == "volume" && m.Name != "" {
					useCount[m.Name] = append(useCount[m.Name], containerName(c))
				}
			}
		}
	}
	data := []map[string]any{}
	for _, v := range out.Volumes {
		cons := useCount[v.Name]
		if cons == nil {
			cons = []string{}
		}
		data = append(data, map[string]any{
			"name": v.Name, "mountpoint": v.Mountpoint, "driver": v.Driver,
			"created": v.CreatedAt,
			"usage":   map[string]any{"ref_count": len(cons), "containers": cons},
		})
	}
	sort.Slice(data, func(i, j int) bool {
		a1, _ := data[i]["name"].(string)
		b1, _ := data[j]["name"].(string)
		return a1 < b1
	})
	ok(w, data)
}

func (a *App) volumeDelete(w http.ResponseWriter, r *http.Request, name string) {
	resp, err := a.Docker.do("DELETE", "/volumes/"+name, nil, nil)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		fail(w, 500, "删除存储卷失败: "+dockerErrMessage(resp))
		return
	}
	a.Logs.Add("WARNING", "删除存储卷 ["+name+"]", "realtime")
	ok(w, nil)
}

func (a *App) volumesPrune(w http.ResponseWriter, r *http.Request) {
	var out map[string]any
	if err := a.Docker.doJSON("POST", "/volumes/prune", nil, map[string]any{}, &out); err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.Logs.Add("SUCCESS", "未使用存储卷清理完成", "realtime")
	ok(w, out)
}

var _ = strings.TrimSpace
