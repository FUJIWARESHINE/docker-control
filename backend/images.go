package main

// 镜像管理 API
import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// handleImagesList GET /api/images
func (a *App) handleImagesList(w http.ResponseWriter, r *http.Request) {
	imgs, err := a.Docker.ListImages(false)
	if err != nil {
		fail(w, 500, "获取镜像列表失败: "+err.Error())
		return
	}
	cs, _ := a.Docker.ListContainers(true)
	// 统计每个 (镜像,标签) 的使用情况
	type usage struct {
		containers []map[string]any
	}
	getContainers := func(u *usage) []map[string]any {
		if u == nil {
			return nil
		}
		return u.containers
	}
	tagUse := map[string]*usage{}        // "repo:tag" -> containers
	imgUse := map[string]bool{}          // imageID -> used
	for _, c := range cs {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		_, icon := a.aliasOf(name)
		cm := map[string]any{"name": name, "icon_path": icon}
		if tagUse[c.Image] == nil {
			tagUse[c.Image] = &usage{}
		}
		tagUse[c.Image].containers = append(getContainers(tagUse[c.Image]), cm)
		imgUse[c.ImageID] = true
	}
	data := []map[string]any{}
	for _, img := range imgs {
		id12 := img.ID
		if len(id12) > 19 {
			id12 = strings.TrimPrefix(id12, "sha256:")[:12]
		}
		created := time.Unix(img.Created, 0).UTC().Format("2006-01-02T15:04:05.000000000Z")
		if len(img.RepoTags) == 0 {
			data = append(data, map[string]any{
				"id": id12, "imageId": id12, "fullTag": "<none>",
				"repoTags": []string{}, "tag_usage": map[string]any{},
				"created": created, "size": img.Size, "used": false,
				"containers": []any{}, "dangling": true,
			})
			continue
		}
		for _, rt := range img.RepoTags {
			u := tagUse[rt]
			used := u != nil && len(u.containers) > 0
			cons := []any{}
			if used {
				for _, c := range u.containers {
					cons = append(cons, c)
				}
			}
			// tag_usage: 该标签对应的容器名列表
			tu := map[string]any{}
			if used {
				names := []string{}
				for _, c := range u.containers {
					if n, okk := c["name"].(string); okk {
						names = append(names, n)
					}
				}
				tu[rt] = names
			}
			data = append(data, map[string]any{
				"id": id12, "imageId": id12, "fullTag": rt,
				"repoTags": img.RepoTags, "tag_usage": tu,
				"created": created, "size": img.Size, "used": used,
				"containers": cons,
			})
		}
	}
	sort.Slice(data, func(i, j int) bool {
		s1, _ := data[i]["fullTag"].(string)
		s2, _ := data[j]["fullTag"].(string)
		return s1 < s2
	})
	ok(w, data)
}

// helper: usage nil-safe containers 已内联到 handleImagesList

// removeImage 删除镜像
func (a *App) removeImage(id string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	resp, err := a.Docker.do("DELETE", "/images/"+id, q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", truncate(string(b), 200))
	}
	return nil
}

// handleImageDelete DELETE /api/images/{id}
func (a *App) handleImageDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/images/")
	force := r.URL.Query().Get("force") == "1"
	if err := a.removeImage(id, force); err != nil {
		fail(w, 500, "删除镜像失败: "+err.Error())
		return
	}
	a.Logs.Add("INFO", "🗑 删除镜像 "+id, "realtime")
	ok(w, nil)
}

// handleImageTag POST /api/images/{id}/tag  {repo, tag}
func (a *App) handleImageTag(w http.ResponseWriter, r *http.Request) {
	parts := strings.TrimPrefix(r.URL.Path, "/api/images/")
	parts = strings.TrimSuffix(parts, "/tag")
	m := readBody(r)
	repo := bodyStr(m, "repo")
	tag := bodyStr(m, "tag")
	if repo == "" {
		fail(w, 400, "缺少 repo")
		return
	}
	if tag == "" {
		tag = "latest"
	}
	q := url.Values{"repo": {repo}, "tag": {tag}}
	resp, err := a.Docker.do("POST", "/images/"+parts+"/tag", q, nil)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		fail(w, 500, truncate(string(b), 200))
		return
	}
	ok(w, nil)
}

// handleImagesPrune POST /api/images/prune  /  /api/images/prune-dangling
func (a *App) handleImagesPrune(w http.ResponseWriter, r *http.Request) {
	danglingOnly := strings.HasSuffix(r.URL.Path, "prune-dangling")
	var reqBody any
	if danglingOnly {
		reqBody = map[string]any{"filters": map[string][]string{"dangling": {"true"}}}
	} else {
		reqBody = map[string]any{}
	}
	var out map[string]any
	if err := a.Docker.doJSON("POST", "/images/prune", url.Values{}, reqBody, &out); err != nil {
		fail(w, 500, "清理失败: "+err.Error())
		return
	}
	a.Logs.Add("SUCCESS", "🧹 镜像清理完成", "realtime")
	ok(w, out)
}

// handleImagePull POST /api/images/pull {image, tag}
func (a *App) handleImagePull(w http.ResponseWriter, r *http.Request) {
	m := readBody(r)
	image := bodyStr(m, "image")
	if image == "" {
		fail(w, 400, "缺少镜像名称")
		return
	}
	tag := bodyStr(m, "tag")
	if tag == "" {
		if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
			tag = image[i+1:]
			image = image[:i]
		} else {
			tag = "latest"
		}
	}
	go a.pullImageStream(image, tag)
	okMsg(w, "拉取任务已启动: "+image+":"+tag)
}

// pullImageStream 后台拉取并推送进度
func (a *App) pullImageStream(image, tag string) {
	q := url.Values{"fromImage": {image}, "tag": {tag}}
	resp, err := a.Docker.do("POST", "/images/create", q, nil)
	if err != nil {
		a.Hub.Broadcast("update_progress", map[string]any{"container": image, "message": "拉取失败: " + err.Error(), "status": "error", "percentage": 0})
		return
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var msg map[string]any
		if err := dec.Decode(&msg); err != nil {
			break
		}
		status, _ := msg["status"].(string)
		id, _ := msg["id"].(string)
		progress, _ := msg["progress"].(string)
		a.Hub.Broadcast("update_progress", map[string]any{
			"container": image, "message": status + " " + id + " " + progress,
			"status": status, "percentage": 0,
		})
	}
	a.Hub.Broadcast("update_progress", map[string]any{"container": image, "message": "✅ 拉取完成", "status": "success", "percentage": 100})
	a.Hub.Broadcast("containers_updated", map[string]any{})
}

// handleImageExport POST /api/images/export {images:[...], path}
func (a *App) handleImageExport(w http.ResponseWriter, r *http.Request) {
	// MVP：不支持服务端导出到任意宿主路径（安全考量），返回提示
	fail(w, 400, "镜像导出请在宿主机使用 docker save 完成")
}

// handleImageImport POST /api/images/import
func (a *App) handleImageImport(w http.ResponseWriter, r *http.Request) {
	fail(w, 400, "镜像导入请在宿主机使用 docker load 完成")
}

// handleImageUpload POST /api/images/upload (multipart tar)
func (a *App) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 30); err != nil {
		fail(w, 400, "上传解析失败: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "缺少文件")
		return
	}
	defer file.Close()
	resp, err := a.Docker.do("POST", "/images/load", nil, nil)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	resp.Body.Close()
	// 重新以流式发送 body
	resp2, err := a.Docker.do("POST", "/images/load", nil, nil)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer resp2.Body.Close()
	// 用上传文件作为 body 重新请求（第一次请求失败也无妨，重来）
	_ = resp2
	dc := a.Docker
	req2, _ := http.NewRequest("POST", dc.base+"/images/load", file)
	req2.Header.Set("Content-Type", "application/x-tar")
	rp, err := dc.client.Do(req2)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer rp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(rp.Body, 1<<20))
	if rp.StatusCode >= 400 {
		fail(w, 500, truncate(string(b), 300))
		return
	}
	okMsg(w, "镜像导入完成")
}
