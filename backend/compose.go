package main

// Compose 项目（基于容器 label 聚合）与相关 API
import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// composeProjects 通过 label com.docker.compose.project 聚合项目
func (a *App) composeProjects() []map[string]any {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return []map[string]any{}
	}
	type proj struct {
		name        string
		composeFile string
		workdir     string
		containers  []map[string]any
		minCreated  int64
	}
	projects := map[string]*proj{}
	for _, c := range cs {
		pn := c.Labels["com.docker.compose.project"]
		if pn == "" {
			continue
		}
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		_, icon := a.aliasOf(name)
		if projects[pn] == nil {
			cf := c.Labels["com.docker.compose.project.config_files"]
			wd := c.Labels["com.docker.compose.project.working_dir"]
			projects[pn] = &proj{name: pn, composeFile: cf, workdir: wd, minCreated: c.Created}
		}
		p := projects[pn]
		if c.Created < p.minCreated {
			p.minCreated = c.Created
		}
		p.containers = append(p.containers, map[string]any{
			"name": name, "state": c.State, "status": c.Status, "icon_path": icon,
		})
	}
	data := []map[string]any{}
	for _, p := range projects {
		state := "stopped"
		running := 0
		for _, c := range p.containers {
			if c["state"] == "running" {
				running++
			}
		}
		if running > 0 {
			state = "running"
		}
		fn := p.composeFile
		if i := strings.LastIndex(fn, ","); i > 0 {
			fn = strings.Split(fn, ",")[0]
		}
		filename := filepath.Base(fn)
		data = append(data, map[string]any{
			"name":            p.name,
			"compose_file":    fn,
			"compose_filename": filename,
			"path":            p.workdir,
			"containers":      p.containers,
			"container_count": len(p.containers),
			"running_count":   running,
			"state":           state,
			"created_at":      fmt.Sprint(p.minCreated),
			"labels":          map[string]string{},
		})
	}
	sort.Slice(data, func(i, j int) bool {
		a1, _ := data[i]["name"].(string)
		b1, _ := data[j]["name"].(string)
		return a1 < b1
	})
	return data
}

// handleComposeProjects GET /api/compose/projects
func (a *App) handleComposeProjects(w http.ResponseWriter, r *http.Request) {
	ok(w, a.composeProjects())
}

// handleComposeAction POST /api/compose/{action}  {project_name}
// start/stop/restart/down
func (a *App) handleComposeAction(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, "/api/compose/")
	m := readBody(r)
	pn := bodyStr(m, "project_name")
	if pn == "" {
		pn = bodyStr(m, "name")
	}
	if pn == "" {
		fail(w, 400, "缺少项目名称")
		return
	}
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	targets := []string{}
	for _, c := range cs {
		if c.Labels["com.docker.compose.project"] == pn {
			targets = append(targets, strings.TrimPrefix(c.Names[0], "/"))
		}
	}
	if len(targets) == 0 {
		fail(w, 404, "项目不存在或没有容器")
		return
	}
	var opErr error
	switch action {
	case "start":
		for _, t := range targets {
			if e := a.Docker.ContainerAction(t, "start", 0); e != nil {
				opErr = e
			}
		}
	case "stop":
		for _, t := range targets {
			if e := a.Docker.ContainerAction(t, "stop", 10); e != nil {
				opErr = e
			}
		}
	case "restart":
		for _, t := range targets {
			if e := a.Docker.ContainerAction(t, "restart", 0); e != nil {
				opErr = e
			}
		}
	case "down":
		// stop + remove 项目容器
		for _, t := range targets {
			a.Docker.ContainerAction(t, "stop", 10)
			a.Docker.RemoveContainer(t, true, false)
		}
	default:
		fail(w, 400, "不支持的操作: "+action)
		return
	}
	if opErr != nil {
		fail(w, 500, action+" 失败: "+opErr.Error())
		return
	}
	a.Logs.Add("INFO", fmt.Sprintf("📦 项目 [%s] %s 完成（%d 个容器）", pn, action, len(targets)), "realtime")
	okMsg(w, "项目 "+pn+" "+action+" 成功")
}

// handleComposeFile GET/POST /api/compose/file  {project_name | path}
func (a *App) handleComposeFile(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	pn := bodyStr(m, "project_name")
	path := bodyStr(m, "path")
	if path == "" && pn != "" {
		for _, p := range a.composeProjects() {
			if p["name"] == pn {
				path, _ = p["compose_file"].(string)
				break
			}
		}
	}
	if path == "" {
		fail(w, 400, "无法定位 compose 文件")
		return
	}
	if r.Method == "GET" {
		b, err := os.ReadFile(path)
		if err != nil {
			fail(w, 500, "读取 compose 文件失败: "+err.Error())
			return
		}
		ok(w, map[string]any{"path": path, "content": string(b)})
		return
	}
	// POST 保存
	content := bodyStr(m, "content")
	if content == "" {
		fail(w, 400, "缺少内容")
		return
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fail(w, 500, "保存失败: "+err.Error())
		return
	}
	a.Logs.Add("INFO", "💾 保存 compose 文件: "+path, "realtime")
	okMsg(w, "保存成功")
}

// handleComposeSave POST /api/compose/save {name, content, path}
func (a *App) handleComposeSave(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	name := bodyStr(m, "name")
	content := bodyStr(m, "content")
	path := bodyStr(m, "path")
	if path == "" {
		if name == "" {
			fail(w, 400, "缺少项目名或路径")
			return
		}
		dir := a.Store.dataDir()
		path = filepath_join(dir, "compose", name+".yml")
	}
	if err := os.MkdirAll(parentDir(path), 0755); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fail(w, 500, "保存失败: "+err.Error())
		return
	}
	ok(w, map[string]any{"path": path})
}

// handleComposeLogs GET /api/compose/logs?project=xxx&tail=200
func (a *App) handleComposeLogs(w http.ResponseWriter, r *http.Request) {
	pn := r.URL.Query().Get("project")
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "100"
	}
	if pn == "" {
		fail(w, 400, "缺少项目名称")
		return
	}
	cs, _ := a.Docker.ListContainers(true)
	var names []string
	for _, c := range cs {
		if c.Labels["com.docker.compose.project"] == pn {
			names = append(names, strings.TrimPrefix(c.Names[0], "/"))
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, n := range names {
		fmt.Fprintf(w, "===== %s =====\n", n)
		q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {tail}}
		resp, err := a.Docker.do("GET", "/containers/"+n+"/logs", q, nil)
		if err != nil {
			continue
		}
		demuxWrite(w, nil, resp.Body)
		resp.Body.Close()
		if len(names) > 1 {
			io.WriteString(w, "\n")
		}
	}
}
