package main

// Compose 项目（label 聚合）：项目列表 / 启停 / 文件读写 / 日志
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

func (a *App) composeProjects() []map[string]any {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return []map[string]any{}
	}
	type proj struct {
		name, composeFile, workdir string
		containers                 []map[string]any
		minCreated                 int64
	}
	projects := map[string]*proj{}
	for _, c := range cs {
		pn := c.Labels["com.docker.compose.project"]
		if pn == "" {
			continue
		}
		name := containerName(c)
		if projects[pn] == nil {
			projects[pn] = &proj{
				name:        pn,
				composeFile: c.Labels["com.docker.compose.project.config_files"],
				workdir:     c.Labels["com.docker.compose.project.working_dir"],
				minCreated:  c.Created,
			}
		}
		p := projects[pn]
		if c.Created < p.minCreated {
			p.minCreated = c.Created
		}
		p.containers = append(p.containers, map[string]any{
			"name": name, "state": c.State, "status": c.Status,
		})
	}
	data := []map[string]any{}
	for _, p := range projects {
		running := 0
		for _, c := range p.containers {
			if c["state"] == "running" {
				running++
			}
		}
		state := "stopped"
		if running > 0 {
			state = "running"
		}
		fn := p.composeFile
		if i := strings.Index(fn, ","); i > 0 {
			fn = fn[:i]
		}
		data = append(data, map[string]any{
			"name":             p.name,
			"compose_file":     fn,
			"compose_filename": filepath.Base(fn),
			"path":             p.workdir,
			"containers":       p.containers,
			"container_count":  len(p.containers),
			"running_count":    running,
			"state":            state,
			"created_at":       fmt.Sprint(p.minCreated),
		})
	}
	sort.Slice(data, func(i, j int) bool {
		a1, _ := data[i]["name"].(string)
		b1, _ := data[j]["name"].(string)
		return a1 < b1
	})
	return data
}

// handleComposeProjects GET /api/compose
func (a *App) handleComposeProjects(w http.ResponseWriter, r *http.Request) {
	ok(w, a.composeProjects())
}

// composeAction POST /api/compose/{project}/{action}
func (a *App) composeAction(w http.ResponseWriter, r *http.Request, project, action string) {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	targets := []string{}
	for _, c := range cs {
		if c.Labels["com.docker.compose.project"] == project {
			targets = append(targets, containerName(c))
		}
	}
	if len(targets) == 0 {
		fail(w, 404, "项目不存在或没有容器")
		return
	}
	var opErr error
	switch action {
	case "start", "restart":
		for _, t := range targets {
			if e := a.Docker.ContainerAction(t, action, 0); e != nil {
				opErr = e
			}
		}
	case "stop":
		for _, t := range targets {
			if e := a.Docker.ContainerAction(t, "stop", 10); e != nil {
				opErr = e
			}
		}
	case "down":
		for _, t := range targets {
			a.Docker.ContainerAction(t, "stop", 10)
			a.Docker.RemoveContainer(t, true)
		}
	default:
		fail(w, 400, "不支持的操作: "+action)
		return
	}
	if opErr != nil {
		fail(w, 500, action+" 失败: "+opErr.Error())
		return
	}
	a.Logs.Add("INFO", fmt.Sprintf("项目 [%s] %s 完成（%d 个容器）", project, action, len(targets)), "realtime")
	writeJSON(w, 200, map[string]any{"success": true, "message": "项目 " + project + " " + action + " 成功"})
}

// composeFile GET/POST /api/compose/file —— GET 用查询参数，POST 用 JSON body
func (a *App) composeFile(w http.ResponseWriter, r *http.Request) {
	var pn, path, content string
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		pn, path = q.Get("project_name"), q.Get("path")
	} else {
		m := readBody(r)
		pn, path, content = bodyStr(m, "project_name"), bodyStr(m, "path"), bodyStr(m, "content")
	}
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
	if r.Method == http.MethodGet {
		b, err := os.ReadFile(path)
		if err != nil {
			fail(w, 500, "读取 compose 文件失败: "+err.Error())
			return
		}
		ok(w, map[string]any{"path": path, "content": string(b)})
		return
	}
	if content == "" {
		fail(w, 400, "缺少内容")
		return
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fail(w, 500, "保存失败: "+err.Error())
		return
	}
	a.Logs.Add("INFO", "保存 compose 文件: "+path, "realtime")
	writeJSON(w, 200, map[string]any{"success": true, "message": "保存成功"})
}

// composeLogs GET /api/compose/logs?project=xxx&tail=200
func (a *App) composeLogs(w http.ResponseWriter, r *http.Request) {
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
			names = append(names, containerName(c))
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, n := range names {
		fmt.Fprintf(w, "===== %s =====\n", n)
		resp, err := a.Docker.doRaw("GET", "/containers/"+n+"/logs",
			url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {tail}})
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
