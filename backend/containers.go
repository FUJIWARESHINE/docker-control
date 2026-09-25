package main

// 容器相关 API：列表/操作/删除/inspect/统计/日志
import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// toPanelContainer 映射为前端期望的字段结构
func (a *App) toPanelContainer(c DockerContainer) map[string]any {
	name := ""
	if len(c.Names) > 0 {
		name = strings.TrimPrefix(c.Names[0], "/")
	}
	repo, tag := parseImage(c.Image)
	isLatest := tag == "latest"
	ports := []map[string]any{}
	for _, p := range c.Ports {
		m := map[string]any{"container_port": p.PrivatePort, "protocol": p.Type}
		if p.PublicPort > 0 {
			m["host_port"] = p.PublicPort
			m["ip"] = p.IP
		}
		ports = append(ports, m)
	}
	autoUpd := a.Store.Data.AutoUpdate[name]
	hasUpd := a.Store.Data.HasUpdate[name]
	updStatus := "unknown"
	if s, okk := a.Store.Data.UpdateStatus[name]; okk {
		updStatus = s
	}
	state := c.State
	if state != "running" {
		state = "stopped"
	}
	alias, icon := a.aliasOf(name)
	return map[string]any{
		"id":            c.ID[:12],
		"name":          name,
		"image":         c.Image,
		"repository":    repo,
		"tag":           tag,
		"is_latest":     isLatest,
		"state":         state,
		"status":        c.Status,
		"created":       time.Unix(c.Created, 0).UTC().Format("2006-01-02T15:04:05.000000000Z"),
		"has_update":    hasUpd,
		"update_status": updStatus,
		"auto_update":   autoUpd,
		"ports":         ports,
		"labels":        c.Labels,
		"alias":         alias,
		"icon":          icon,
	}
}

func (a *App) aliasOf(name string) (string, string) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	if v, okk := a.Store.Data.Aliases[name]; okk {
		if m, ok2 := v.(map[string]any); ok2 {
			al, _ := m["alias"].(string)
			ic, _ := m["icon_path"].(string)
			return al, ic
		}
	}
	return "", ""
}

func parseImage(img string) (repo, tag string) {
	// 处理 registry 端口: host:port/name:tag
	i := strings.LastIndex(img, ":")
	if i == -1 {
		return img, "latest"
	}
	if strings.Contains(img[i:], "/") {
		return img, "latest"
	}
	return img[:i], img[i+1:]
}

// handleContainersList GET /api/containers
func (a *App) handleContainersList(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true" || r.URL.Query().Get("all") == "1"
	cs, err := a.Docker.ListContainers(all)
	if err != nil {
		fail(w, 500, "获取容器列表失败: "+err.Error())
		return
	}
	data := []map[string]any{}
	for _, c := range cs {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		// 面板自身：API 层直接剔除（更安全——列表里根本不存在，无法误操作）
		if c.Labels["diancup.self"] == "true" || name == a.SelfName {
			continue
		}
		data = append(data, a.toPanelContainer(c))
	}
	a.Store.mu.RLock()
	lastCheck := a.Store.Data.LastCheck
	a.Store.mu.RUnlock()
	writeJSON(w, 200, map[string]any{"success": true, "data": data, "last_check": lastCheck})
}

// handleContainerAction POST /api/container/{start|stop|restart}
func (a *App) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, "/api/container/")
	m := readBody(r)
	name := bodyStr(m, "container_name")
	if name == "" {
		fail(w, 400, "缺少容器名称")
		return
	}
	switch action {
	case "start", "stop", "restart", "kill", "pause", "unpause":
	default:
		fail(w, 400, "不支持的操作: "+action)
		return
	}
	timeout := 10
	if action == "stop" {
		timeout = 10
	}
	if err := a.Docker.ContainerAction(name, action, timeout); err != nil {
		fail(w, 500, action+" 失败: "+err.Error())
		return
	}
	a.Logs.Add("INFO", fmt.Sprintf("%s 容器 [%s] 成功", map[string]string{"start": "▶ 启动", "stop": "⏹ 停止", "restart": "🔄 重启"}[action], name), "realtime")
	ok(w, nil)
}

// handleContainerDelete POST /api/container/delete
func (a *App) handleContainerDelete(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	name := bodyStr(m, "container_name")
	if name == "" {
		fail(w, 400, "缺少容器名称")
		return
	}
	delImage := bodyBool(m, "delete_image")
	// 先记录镜像名用于可选删除
	var imgID string
	var cs, _ = a.Docker.ListContainers(true)
	for _, c := range cs {
		if len(c.Names) > 0 && strings.TrimPrefix(c.Names[0], "/") == name {
			imgID = c.ImageID
		}
	}
	if err := a.Docker.RemoveContainer(name, true, false); err != nil {
		fail(w, 500, "删除容器失败: "+err.Error())
		return
	}
	if delImage && imgID != "" {
		a.removeImage(imgID, false)
	}
	a.Logs.Add("WARNING", "🗑 删除容器 ["+name+"]", "realtime")
	ok(w, nil)
}

// handleContainerInspect GET /api/containers/{id}/inspect
func (a *App) handleContainerInspect(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/containers/"), "/inspect")
	var out map[string]any
	if err := a.Docker.doJSON("GET", "/containers/"+id+"/json", nil, nil, &out); err != nil {
		fail(w, 500, err.Error())
		return
	}
	ok(w, out)
}

// handleContainerCreate POST /api/containers/create
func (a *App) handleContainerCreate(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	spec, _ := m["container"].(map[string]any)
	if spec == nil {
		spec = m
	}
	var created map[string]any
	if err := a.Docker.doJSON("POST", "/containers/create", url.Values{"name": {bodyStr(spec, "name")}}, spec, &created); err != nil {
		fail(w, 500, "创建容器失败: "+err.Error())
		return
	}
	if startErr := a.Docker.ContainerAction(bodyStr(created, "Id"), "start", 0); startErr != nil {
		okMsg(w, "容器已创建但启动失败: "+startErr.Error())
		return
	}
	ok(w, created)
}

// ---------- 统计 ----------

type statsBatchItem struct {
	ContainerID string `json:"container_id"`
	Success     bool   `json:"success"`
	CPUPercent  string `json:"cpu_percent"`
	MemUsage    string `json:"mem_usage"`    // 字节
	MemPercent  string `json:"mem_percent"`
	NetRxRate   string `json:"net_rx_rate"`  // 字节/秒
	NetTxRate   string `json:"net_tx_rate"`
	Error       string `json:"error,omitempty"`
}

// handleStatsBatch GET /api/containers/stats/batch
func (a *App) handleStatsBatch(w http.ResponseWriter, r *http.Request) {
	cs, err := a.Docker.ListContainers(false)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	stats := []statsBatchItem{}
	for _, c := range cs {
		item := a.oneShotStats(c)
		stats = append(stats, item)
	}
	writeJSON(w, 200, map[string]any{"success": true, "stats": stats})
}

// oneShotStats 单次采样计算 CPU%（利用 precpu_stats）
func (a *App) oneShotStats(c DockerContainer) statsBatchItem {
	item := statsBatchItem{ContainerID: c.ID[:12], Success: false}
	var st struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64   `json:"system_cpu_usage"`
			OnlineCPUs     uint32   `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		MemoryStats struct {
			Usage uint64 `json:"usage"`
			Limit uint64 `json:"limit"`
			Stats map[string]uint64 `json:"stats"`
		} `json:"memory_stats"`
		Networks map[string]struct {
			RxBytes uint64 `json:"rx_bytes"`
			TxBytes uint64 `json:"tx_bytes"`
		} `json:"networks"`
	}
	if err := a.Docker.doJSON("GET", "/containers/"+c.ID+"/stats", url.Values{"stream": {"false"}, "one-shot": {"false"}}, nil, &st); err != nil {
		item.Error = err.Error()
		return item
	}
	item.Success = true
	// CPU%
	cpuDelta := float64(st.CPUStats.CPUUsage.TotalUsage) - float64(st.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(st.CPUStats.SystemCPUUsage) - float64(st.PreCPUStats.SystemCPUUsage)
	if sysDelta > 0 && cpuDelta > 0 {
		ncpu := float64(st.CPUStats.OnlineCPUs)
		if ncpu == 0 {
			ncpu = 1
		}
		item.CPUPercent = strconv.FormatFloat(cpuDelta/sysDelta*ncpu*100, 'f', 2, 64)
	} else {
		item.CPUPercent = "0.00"
	}
	// 内存
	mem := float64(st.MemoryStats.Usage)
	if cache, okk := st.MemoryStats.Stats["cache"]; okk {
		mem -= float64(cache)
	} else if inactive, ok2 := st.MemoryStats.Stats["inactive_file"]; ok2 {
		mem -= float64(inactive)
	}
	if st.MemoryStats.Limit > 0 {
		item.MemUsage = strconv.FormatUint(uint64(mem), 10)
		item.MemPercent = strconv.FormatFloat(mem/float64(st.MemoryStats.Limit)*100, 'f', 2, 64)
	} else {
		item.MemUsage = "0"
		item.MemPercent = "0.00"
	}
	// 网络（host 模式容器 engine 不提供 networks，为 0）
	var rx, tx uint64
	for _, n := range st.Networks {
		rx += n.RxBytes
		tx += n.TxBytes
	}
	item.NetRxRate = "0"
	item.NetTxRate = "0"
	_ = rx
	_ = tx
	return item
}

// ---------- 日志 ----------

// handleContainerLogs GET /api/container/logs?name=xxx&tail=200&follow=0
func (a *App) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	tail := r.URL.Query().Get("tail")
	follow := r.URL.Query().Get("follow") == "1"
	if name == "" {
		fail(w, 400, "缺少容器名称")
		return
	}
	q := url.Values{}
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	q.Set("timestamps", "1")
	if tail == "" {
		tail = "300"
	}
	q.Set("tail", tail)
	if follow {
		q.Set("follow", "1")
	}
	resp, err := a.Docker.do("GET", "/containers/"+name+"/logs", q, nil)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		fail(w, 500, truncate(string(b), 300))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	flusher, _ := w.(http.Flusher)
	demuxWrite(w, flusher, resp.Body)
}

// demuxWrite 解多路 Docker 日志帧（8 字节头）并写入
func demuxWrite(w io.Writer, flusher http.Flusher, rd io.Reader) {
	buf := make([]byte, 8)
	var cur []byte
	for {
		if _, err := io.ReadFull(rd, buf); err != nil {
			if len(cur) > 0 {
				w.Write(cur)
			}
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		if buf[0] != 1 && buf[0] != 2 {
			// 非多路复用流（TTY 模式），直接透传
			w.Write(buf)
			io.Copy(w, rd)
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		n := int(buf[4])<<24 | int(buf[5])<<16 | int(buf[6])<<8 | int(buf[7])
		if n <= 0 {
			continue
		}
		chunk := make([]byte, n)
		if _, err := io.ReadFull(rd, chunk); err != nil {
			return
		}
		cur = append(cur, chunk...)
		// 按行输出
		for {
			idx := indexByte(cur, '\n')
			if idx == -1 {
				break
			}
			w.Write(cur[:idx+1])
			cur = cur[idx+1:]
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// ---------- 容器生成 compose ----------

// handleGenerateCompose POST /api/container/generate-compose
func (a *App) handleGenerateCompose(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	name := bodyStr(m, "container_name")
	if name == "" {
		fail(w, 400, "缺少容器名称")
		return
	}
	var insp struct {
		Name    string `json:"Name"`
		Config  map[string]any `json:"Config"`
		HostConfig map[string]any `json:"HostConfig"`
	}
	if err := a.Docker.doJSON("GET", "/containers/"+name+"/json", nil, nil, &insp); err != nil {
		fail(w, 500, err.Error())
		return
	}
	y := generateComposeYAML(name, insp.Config, insp.HostConfig)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeJSON(w, 200, map[string]any{"success": true, "compose": y, "data": y})
}

// generateComposeYAML 从 inspect 配置生成近似 compose YAML
func generateComposeYAML(name string, config, hostConfig map[string]any) string {
	var b strings.Builder
	svc := sanitizeService(name)
	b.WriteString("services:\n")
	b.WriteString("  " + svc + ":\n")
	if img, okk := config["Image"].(string); okk {
		b.WriteString("    image: " + img + "\n")
	}
	if name != svc {
		b.WriteString("    container_name: " + name + "\n")
	}
	if rst, okk := hostConfig["RestartPolicy"].(map[string]any); okk {
		if rn, _ := rst["Name"].(string); rn != "" && rn != "no" {
			b.WriteString("    restart: " + rn + "\n")
		}
	}
	// 端口
	if binds, okk := hostConfig["PortBindings"].(map[string]any); okk && len(binds) > 0 {
		b.WriteString("    ports:\n")
		for k, v := range binds {
			arr, _ := v.([]any)
			for _, item := range arr {
				pm, _ := item.(map[string]any)
				hostPort, _ := pm["HostPort"].(string)
				if hostPort != "" {
					b.WriteString("      - \"" + hostPort + ":" + strings.TrimSuffix(k, "/tcp") + "\"\n")
				}
			}
		}
	}
	// 卷
	if binds, okk := hostConfig["Binds"].([]any); okk {
		b.WriteString("    volumes:\n")
		for _, v := range binds {
			if s, ok2 := v.(string); ok2 {
				b.WriteString("      - \"" + s + "\"\n")
			}
		}
	}
	// 环境
	if envs, okk := config["Env"].([]any); okk {
		b.WriteString("    environment:\n")
		for _, v := range envs {
			if s, ok2 := v.(string); ok2 {
				b.WriteString("      - " + s + "\n")
			}
		}
	}
	// 网络
	if nets, okk := hostConfig["NetworkMode"].(string); okk && nets != "" && nets != "default" {
		b.WriteString("    network_mode: " + nets + "\n")
	}
	return b.String()
}

func sanitizeService(name string) string {
	s := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return -1
	}, strings.ToLower(name))
	if s == "" {
		s = "service"
	}
	return s
}

// ---------- 排序辅助 ----------

func sortByName(data []map[string]any) {
	sort.Slice(data, func(i, j int) bool {
		a1, _ := data[i]["name"].(string)
		b1, _ := data[j]["name"].(string)
		return a1 < b1
	})
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ = time.Now
