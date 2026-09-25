package main

// 容器：列表 / 操作 / 删除 / inspect / 统计 / 日志 / 创建 / 生成 compose
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- 列表 ----------

// toPanelItem 映射为前端契约字段
func (a *App) toPanelItem(c DockerContainer) map[string]any {
	name := containerName(c)
	repo, tag := parseImage(c.Image)
	ports := []map[string]any{}
	for _, p := range c.Ports {
		m := map[string]any{"container_port": p.PrivatePort, "protocol": p.Type}
		if p.PublicPort > 0 {
			m["host_port"] = p.PublicPort
			m["ip"] = p.IP
		}
		ports = append(ports, m)
	}
	state := c.State
	if state != "running" {
		state = "stopped"
	}
	alias, _ := a.aliasOf(name)
	a.Store.mu.RLock()
	autoUpd := a.Store.Data.AutoUpdate[name]
	hasUpd := a.Store.Data.HasUpdate[name]
	updStatus := a.Store.Data.UpdateStatus[name]
	a.Store.mu.RUnlock()
	if updStatus == "" {
		updStatus = "unknown"
	}
	return map[string]any{
		"id":              c.ID[:12],
		"name":            name,
		"image":           c.Image,
		"repository":      repo,
		"tag":             tag,
		"is_latest":       tag == "latest",
		"state":           state,
		"status":          c.Status,
		"created":         time.Unix(c.Created, 0).UTC().Format("2006-01-02T15:04:05.000000000Z"),
		"has_update":      hasUpd,
		"update_status":   updStatus,
		"auto_update":     autoUpd,
		"ports":           ports,
		"labels":          c.Labels,
		"alias":           alias,
		"compose_project": c.Labels["com.docker.compose.project"],
	}
}

func parseImage(img string) (repo, tag string) {
	i := strings.LastIndex(img, ":")
	if i == -1 || strings.Contains(img[i:], "/") {
		return img, "latest"
	}
	return img[:i], img[i+1:]
}

func (a *App) aliasOf(name string) (string, string) {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	if v, ok := a.Store.Data.Aliases[name]; ok {
		if m, ok2 := v.(map[string]any); ok2 {
			al, _ := m["alias"].(string)
			ic, _ := m["icon_path"].(string)
			return al, ic
		}
	}
	return "", ""
}

// handleContainers GET/POST /api/containers —— GET 列表，POST 创建
func (a *App) handleContainersList(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.handleContainerCreate(w, r)
		return
	}
	all := r.URL.Query().Get("all") == "1" || r.URL.Query().Get("all") == "true"
	cs, err := a.Docker.ListContainers(all)
	if err != nil {
		fail(w, 500, "获取容器列表失败: "+err.Error())
		return
	}
	data := []map[string]any{}
	for _, c := range cs {
		name := containerName(c)
		// 自保护：面板自身容器在 API 层剔除（列表里根本不存在，无法误操作）
		if c.Labels["docker-control.self"] == "true" || name == a.SelfName {
			continue
		}
		data = append(data, a.toPanelItem(c))
	}
	a.Store.mu.RLock()
	lastCheck := a.Store.Data.LastCheck
	a.Store.mu.RUnlock()
	writeJSON(w, 200, map[string]any{"success": true, "data": data, "last_check": lastCheck})
}

// ---------- 操作 / 删除 ----------

func (a *App) containerAction(w http.ResponseWriter, r *http.Request, id, action string) {
	m := readBody(r)
	name := bodyStr(m, "container_name")
	if name == "" {
		name = id
	}
	if err := a.Docker.ContainerAction(name, action, 10); err != nil {
		fail(w, 500, action+" 失败: "+err.Error())
		return
	}
	a.Logs.Add("INFO", fmt.Sprintf("%s 容器 [%s] 成功", map[string]string{
		"start": "启动", "stop": "停止", "restart": "重启",
		"kill": "强杀", "pause": "暂停", "unpause": "恢复",
	}[action], name), "realtime")
	ok(w, nil)
}

func (a *App) containerDelete(w http.ResponseWriter, r *http.Request, id string) {
	m := readBody(r)
	name := bodyStr(m, "container_name")
	if name == "" {
		name = id
	}
	delImage := bodyBool(m, "delete_image")
	var imgID string
	if cs, err := a.Docker.ListContainers(true); err == nil {
		for _, c := range cs {
			if containerName(c) == name || strings.HasPrefix(c.ID, id) {
				imgID = c.ImageID
			}
		}
	}
	if err := a.Docker.RemoveContainer(name, true); err != nil {
		fail(w, 500, "删除容器失败: "+err.Error())
		return
	}
	if delImage && imgID != "" {
		a.removeImage(imgID, false)
	}
	a.Logs.Add("WARNING", "删除容器 ["+name+"]", "realtime")
	ok(w, nil)
}

// handleContainersCreate POST /api/containers  —— body 直接作为 Engine create spec
func (a *App) handleContainerCreate(w http.ResponseWriter, r *http.Request) {
	spec := readBody(r)
	var created map[string]any
	if err := a.Docker.doJSON("POST", "/containers/create", url.Values{"name": {bodyStr(spec, "name")}}, spec, &created); err != nil {
		fail(w, 500, "创建容器失败: "+err.Error())
		return
	}
	if id, _ := created["Id"].(string); id != "" {
		if startErr := a.Docker.ContainerAction(id, "start", 0); startErr != nil {
			writeJSON(w, 200, map[string]any{"success": true, "message": "容器已创建但启动失败: " + startErr.Error(), "data": created})
			return
		}
	}
	ok(w, created)
}

// ---------- inspect ----------

func (a *App) containerInspect(w http.ResponseWriter, r *http.Request, id string) {
	var raw map[string]any
	if err := a.Docker.doJSON("GET", "/containers/"+id+"/json", nil, nil, &raw); err != nil {
		fail(w, 500, err.Error())
		return
	}
	merged := map[string]any{}
	for k, v := range raw {
		merged[k] = v
	}
	// 面板契约字段覆盖（与列表一致，前端直接渲染）
	if panel := a.panelContractFor(id); panel != nil {
		for k, v := range panel {
			merged[k] = v
		}
	} else if sid, _ := raw["Id"].(string); len(sid) >= 12 {
		merged["id"] = sid[:12]
		if name, _ := raw["Name"].(string); name != "" {
			merged["name"] = strings.TrimPrefix(name, "/")
		}
		if st, _ := raw["State"].(map[string]any); st != nil {
			if s, _ := st["Status"].(string); s == "running" {
				merged["state"] = "running"
			} else {
				merged["state"] = "stopped"
			}
		}
	}
	a.deriveInspectFields(raw, merged)
	ok(w, merged)
}

func (a *App) panelContractFor(idOrName string) map[string]any {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return nil
	}
	for _, c := range cs {
		if c.ID == idOrName || strings.HasPrefix(c.ID, idOrName) || containerName(c) == idOrName {
			return a.toPanelItem(c)
		}
	}
	return nil
}

// deriveInspectFields 拍平前端直接可读的派生字段
func (a *App) deriveInspectFields(raw, merged map[string]any) {
	config, _ := raw["Config"].(map[string]any)
	hostCfg, _ := raw["HostConfig"].(map[string]any)
	netSet, _ := raw["NetworkSettings"].(map[string]any)
	if config == nil {
		config = map[string]any{}
	}
	if hostCfg == nil {
		hostCfg = map[string]any{}
	}
	if netSet == nil {
		netSet = map[string]any{}
	}
	for dst, key := range map[string]string{"command": "Cmd", "entrypoint": "Entrypoint", "env": "Env"} {
		if v, ok := config[key].([]any); ok {
			merged[dst] = v
		} else {
			merged[dst] = []any{}
		}
	}
	merged["hostname"], _ = config["Hostname"].(string)
	merged["working_dir"], _ = config["WorkingDir"].(string)

	rp := map[string]any{"Name": "no", "MaximumRetryCount": 0}
	if rpm, _ := hostCfg["RestartPolicy"].(map[string]any); rpm != nil {
		if n, _ := rpm["Name"].(string); n != "" {
			rp["Name"] = n
		}
		if m, _ := rpm["MaximumRetryCount"].(float64); m > 0 {
			rp["MaximumRetryCount"] = int(m)
		}
	}
	merged["restart_policy"] = rp

	ports := []map[string]any{}
	if ns, _ := netSet["Ports"].(map[string]any); ns != nil {
		for k, v := range ns {
			proto, cport := "tcp", k
			if i := strings.Index(k, "/"); i > 0 {
				proto, cport = k[i+1:], k[:i]
			}
			arr, _ := v.([]any)
			for _, item := range arr {
				pm, _ := item.(map[string]any)
				ports = append(ports, map[string]any{
					"host_ip":        pm["HostIp"],
					"host_port":      pm["HostPort"],
					"container_port": cport,
					"protocol":       proto,
				})
			}
		}
	}
	merged["ports"] = ports

	mounts := []map[string]any{}
	if ml, _ := raw["Mounts"].([]any); ml != nil {
		for _, item := range ml {
			mm, _ := item.(map[string]any)
			mounts = append(mounts, map[string]any{
				"type": mm["Type"], "source": mm["Source"],
				"destination": mm["Destination"], "mode": mm["Mode"],
				"rw": mm["RW"], "propagation": mm["Propagation"],
			})
		}
	}
	merged["mounts"] = mounts

	networks := []map[string]any{}
	if nsm, _ := netSet["Networks"].(map[string]any); nsm != nil {
		names := make([]string, 0, len(nsm))
		for k := range nsm {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			nm, _ := nsm[k].(map[string]any)
			networks = append(networks, map[string]any{
				"name": k, "ip_address": nm["IPAddress"], "gateway": nm["Gateway"],
			})
		}
	}
	merged["network"] = map[string]any{"networks": networks}
}

// ---------- 统计（批量 one-shot 采样 + 后端差分缓存） ----------

type statsBatchItem struct {
	ContainerID string `json:"container_id"`
	Name        string `json:"name"`
	Success     bool   `json:"success"`
	CPUPercent  string `json:"cpu_percent"`
	MemUsage    string `json:"mem_usage"`
	MemPercent  string `json:"mem_percent"`
	NetRxRate   string `json:"net_rx_rate"`
	NetTxRate   string `json:"net_tx_rate"`
	Error       string `json:"error,omitempty"`
}

type statsPrev struct {
	ts       time.Time
	cpu, sys uint64
	rx, tx   uint64
}

var (
	statsMu      sync.Mutex
	statsPrevMap = map[string]*statsPrev{}
)

type statsRaw struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

// handleStatsBatch GET /api/stats —— 并发采样全部运行中容器
func (a *App) handleStatsBatch(w http.ResponseWriter, r *http.Request) {
	cs, err := a.Docker.ListContainers(false)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	items := make([]statsBatchItem, len(cs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, c := range cs {
		wg.Add(1)
		go func(i int, c DockerContainer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			items[i] = a.sampleStats(c)
		}(i, c)
	}
	wg.Wait()
	stats := []statsBatchItem{}
	for _, it := range items {
		stats = append(stats, it)
	}
	writeJSON(w, 200, map[string]any{"success": true, "stats": stats})
}

func (a *App) statsRequest(id string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	q := url.Values{"stream": {"false"}, "one-shot": {"true"}}
	req, err := http.NewRequestWithContext(ctx, "GET", a.Docker.base+"/containers/"+id+"/stats?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := a.Docker.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("stats http %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	return json.Unmarshal(data, out)
}

func (a *App) sampleStats(c DockerContainer) statsBatchItem {
	key := c.ID
	if len(key) > 12 {
		key = key[:12]
	}
	item := statsBatchItem{ContainerID: key, Name: containerName(c)}
	var st statsRaw
	if err := a.statsRequest(c.ID, &st); err != nil {
		item.Error = err.Error()
		return item
	}
	item.Success = true
	now := time.Now()
	mem := float64(st.MemoryStats.Usage)
	if cache, ok := st.MemoryStats.Stats["cache"]; ok {
		mem -= float64(cache)
	} else if inactive, ok2 := st.MemoryStats.Stats["inactive_file"]; ok2 {
		mem -= float64(inactive)
	}
	if st.MemoryStats.Limit > 0 {
		item.MemUsage = strconv.FormatUint(uint64(mem), 10)
		item.MemPercent = strconv.FormatFloat(mem/float64(st.MemoryStats.Limit)*100, 'f', 2, 64)
	} else {
		item.MemUsage, item.MemPercent = "0", "0.00"
	}
	var rx, tx uint64
	for _, n := range st.Networks {
		rx += n.RxBytes
		tx += n.TxBytes
	}
	statsMu.Lock()
	prev := statsPrevMap[key]
	statsPrevMap[key] = &statsPrev{ts: now, cpu: st.CPUStats.CPUUsage.TotalUsage, sys: st.CPUStats.SystemCPUUsage, rx: rx, tx: tx}
	statsMu.Unlock()
	if prev != nil {
		item.CPUPercent = strconv.FormatFloat(cpuPercentOf(prev.cpu, st.CPUStats.CPUUsage.TotalUsage,
			prev.sys, st.CPUStats.SystemCPUUsage, st.CPUStats.OnlineCPUs), 'f', 2, 64)
		if dt := now.Sub(prev.ts).Seconds(); dt > 0.2 {
			item.NetRxRate = strconv.FormatFloat(float64(rx-prev.rx)/dt, 'f', 0, 64)
			item.NetTxRate = strconv.FormatFloat(float64(tx-prev.tx)/dt, 'f', 0, 64)
		} else {
			item.NetRxRate, item.NetTxRate = "0", "0"
		}
		return item
	}
	item.CPUPercent = strconv.FormatFloat(cpuPercentOf(st.PreCPUStats.CPUUsage.TotalUsage, st.CPUStats.CPUUsage.TotalUsage,
		st.PreCPUStats.SystemCPUUsage, st.CPUStats.SystemCPUUsage, st.CPUStats.OnlineCPUs), 'f', 2, 64)
	item.NetRxRate, item.NetTxRate = "0", "0"
	return item
}

func cpuPercentOf(cpu0, cpu1, sys0, sys1 uint64, ncpu uint32) float64 {
	cpuDelta := float64(cpu1) - float64(cpu0)
	sysDelta := float64(sys1) - float64(sys0)
	if sysDelta <= 0 || cpuDelta <= 0 {
		return 0
	}
	if ncpu == 0 {
		ncpu = 1
	}
	return cpuDelta / sysDelta * float64(ncpu) * 100
}

// ---------- 日志 ----------

func (a *App) containerLogs(w http.ResponseWriter, r *http.Request, id string) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = id
	}
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "300"
	}
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "tail": {tail}}
	if r.URL.Query().Get("follow") == "1" {
		q.Set("follow", "1")
	}
	resp, err := a.Docker.doRaw("GET", "/containers/"+name+"/logs", q)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	demuxWrite(w, w.(http.Flusher), resp.Body)
}

// demuxWrite 解多路 Docker 日志帧（8 字节头）按行输出
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

// ---------- 生成 compose ----------

func (a *App) containerGenerateCompose(w http.ResponseWriter, r *http.Request, id string) {
	var insp struct {
		Name       string         `json:"Name"`
		Config     map[string]any `json:"Config"`
		HostConfig map[string]any `json:"HostConfig"`
	}
	if err := a.Docker.doJSON("GET", "/containers/"+id+"/json", nil, nil, &insp); err != nil {
		fail(w, 500, err.Error())
		return
	}
	name := strings.TrimPrefix(insp.Name, "/")
	y := generateComposeYAML(name, insp.Config, insp.HostConfig)
	ok(w, map[string]any{"compose": y})
}

func generateComposeYAML(name string, config, hostConfig map[string]any) string {
	var b strings.Builder
	svc := sanitizeService(name)
	b.WriteString("services:\n  " + svc + ":\n")
	if img, _ := config["Image"].(string); img != "" {
		b.WriteString("    image: " + img + "\n")
	}
	if name != svc {
		b.WriteString("    container_name: " + name + "\n")
	}
	if rst, _ := hostConfig["RestartPolicy"].(map[string]any); rst != nil {
		if rn, _ := rst["Name"].(string); rn != "" && rn != "no" {
			b.WriteString("    restart: " + rn + "\n")
		}
	}
	if binds, ok := hostConfig["PortBindings"].(map[string]any); ok && len(binds) > 0 {
		b.WriteString("    ports:\n")
		for k, v := range binds {
			arr, _ := v.([]any)
			for _, item := range arr {
				pm, _ := item.(map[string]any)
				if hp, _ := pm["HostPort"].(string); hp != "" {
					b.WriteString("      - \"" + hp + ":" + strings.TrimSuffix(k, "/tcp") + "\"\n")
				}
			}
		}
	}
	if binds, ok := hostConfig["Binds"].([]any); ok {
		b.WriteString("    volumes:\n")
		for _, v := range binds {
			if s, ok2 := v.(string); ok2 {
				b.WriteString("      - \"" + s + "\"\n")
			}
		}
	}
	if envs, ok := config["Env"].([]any); ok && len(envs) > 0 {
		b.WriteString("    environment:\n")
		for _, v := range envs {
			if s, ok2 := v.(string); ok2 {
				b.WriteString("      - " + s + "\n")
			}
		}
	}
	if nets, _ := hostConfig["NetworkMode"].(string); nets != "" && nets != "default" {
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
