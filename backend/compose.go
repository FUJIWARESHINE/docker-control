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
		hasSelf                    bool // 项目内含面板自身容器
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
		// 标记含面板自身的项目，供前端隐藏启停按钮、后端拒绝项目级生命周期操作
		if c.Labels["docker-control.self"] == "true" || name == a.SelfName {
			p.hasSelf = true
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
			"self":             p.hasSelf,
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
//
// 动作：start / stop / restart / down / status
// 自保护：项目内含面板自身容器（docker-control.self=true）时，拒绝一切生命
// 周期操作。容器级接口（containerAction / containerDelete）已有同样的 403 保护，
// 但项目级是按 compose label 聚合的，绕过了那层校验 —— 其中 down 会直接移除
// 面板自身的容器，而 restart:always 对「已删除」的容器无效，面板将永久消失；
// stop / restart 则会打断正在处理这次请求的进程（表现为连接被重置）。
func (a *App) composeAction(w http.ResponseWriter, r *http.Request, project, action string) {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	targets := []string{}
	hasSelf := false
	for _, c := range cs {
		if c.Labels["com.docker.compose.project"] == project {
			name := containerName(c)
			if c.Labels["docker-control.self"] == "true" || name == a.SelfName {
				hasSelf = true
			}
			targets = append(targets, name)
		}
	}
	if len(targets) == 0 {
		fail(w, 404, "项目不存在或没有容器")
		return
	}
	if hasSelf {
		a.Logs.Add("WARNING", fmt.Sprintf("已拦截 Compose 项目 [%s] 的 %s 操作（项目含面板自身容器）", project, action), "realtime")
		fail(w, 403, "自保护：项目 "+project+" 包含 docker-control 面板自身容器，禁止执行 "+action+
			"。如需更新面板请使用「更新中心」的重建式更新，或在宿主机执行 docker compose 命令。")
		return
	}
	var opErr error
	switch action {
	case "status":
		// 只读：返回项目概览，不做任何变更
		running := 0
		for _, c := range cs {
			if c.Labels["com.docker.compose.project"] == project && c.State == "running" {
				running++
			}
		}
		ok(w, map[string]any{"project": project, "containers": targets, "running_count": running})
		return
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

// composeDirs 返回允许面板读写 compose 文件的宿主机目录白名单。
//
// 来源：环境变量 COMPOSE_DIRS（多个用英文冒号分隔）。
// 部署时把这些目录「同路径」挂载进容器，容器内的路径与宿主机完全一致，
// 因此面板可以直接按宿主机路径读写，无需前缀转换。
//
// 未配置时返回空切片，表示不限制目录（兼容旧的全挂载 /:/host:rw 方案）。
func composeDirs() []string {
	raw := envOr("COMPOSE_DIRS", "")
	if raw == "" {
		return nil
	}
	var out []string
	for _, d := range strings.Split(raw, ":") {
		d = strings.TrimSpace(d)
		if d != "" {
			out = append(out, filepath.Clean(d))
		}
	}
	return out
}

// allowedComposePath 校验 p 是否位于白名单目录内。
// 白名单为空时放行（未配置即不限制）。
// 使用 filepath.Clean + 前缀比较，防止 ../ 逃逸。
func allowedComposePath(p string) bool {
	dirs := composeDirs()
	if len(dirs) == 0 {
		return true
	}
	clean := filepath.Clean(p)
	for _, d := range dirs {
		if clean == d || strings.HasPrefix(clean, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveHostPath 把「宿主机视角的路径」解析为「容器内可读写的路径」。
//
// 背景：面板运行在容器里，而 compose 文件的路径来自容器的
// com.docker.compose.project.config_files label，那是宿主机视角的路径。
// 若容器未挂载宿主机文件系统，os.ReadFile 会直接 ENOENT（这是 Compose 编辑报错的根因）。
//
// 支持两种部署方式：
//  1. 窄挂载（推荐）：把 compose 目录「同路径」挂进容器，
//     容器内路径 == 宿主机路径，直接可用，无需任何转换。
//  2. 根挂载（兼容）：挂 /:/host:rw 并设置 HOST_ROOT=/host，
//     把宿主机路径映射到 /host 前缀下。
//
// 返回 (实际可用路径, 是否找到)。找不到时调用方应给出明确的部署提示。
func resolveHostPath(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	// 0) 白名单校验：只允许操作配置内的 compose 目录
	if !allowedComposePath(p) {
		return "", false
	}
	// 1) 原路径直接可用（窄挂载 / 未容器化运行 / 恰好路径一致）
	if _, err := os.Stat(p); err == nil {
		return p, true
	}
	// 2) 尝试映射到 HOST_ROOT 前缀（旧的全挂载方案）
	root := envOr("HOST_ROOT", "")
	if root != "" && strings.HasPrefix(p, "/") {
		candidate := filepath.Join(root, p)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
		// 文件可能尚不存在（新建场景），但父目录存在就允许写入
		if _, err := os.Stat(filepath.Dir(candidate)); err == nil {
			return candidate, true
		}
	}
	return "", false
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

	// 路径映射：宿主机路径 → 容器内路径
	realPath, found := resolveHostPath(path)
	if !found {
		if !allowedComposePath(path) {
			fail(w, 403, fmt.Sprintf(
				"路径 %s 不在允许的 Compose 目录内。当前白名单：%s。"+
					"如需操作该目录，请调整部署时的 COMPOSE_DIRS 环境变量与挂载配置。",
				path, strings.Join(composeDirs(), ", ")))
			return
		}
		fail(w, 500, fmt.Sprintf(
			"读取 compose 文件失败：容器内无法访问宿主机路径 %s。"+
				"请把该 compose 所在目录「同路径」挂载进容器（例如 -v /srv/docker:/srv/docker:rw）"+
				"并设置 COMPOSE_DIRS=/srv/docker", path))
		return
	}

	if r.Method == http.MethodGet {
		b, err := os.ReadFile(realPath)
		if err != nil {
			fail(w, 500, "读取 compose 文件失败: "+err.Error())
			return
		}
		ok(w, map[string]any{"path": path, "content": string(b), "resolved": realPath})
		return
	}
	if content == "" {
		fail(w, 400, "缺少内容")
		return
	}
	if err := os.WriteFile(realPath, []byte(content), 0644); err != nil {
		fail(w, 500, "保存失败: "+err.Error())
		return
	}
	a.Logs.Add("INFO", "保存 compose 文件: "+realPath, "realtime")
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
