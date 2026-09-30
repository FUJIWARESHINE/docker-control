package main

// 镜像：列表 / tag / 删除 / 清理 / 拉取（WS 进度）/ 导入
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
	imgs, err := a.Docker.ListImages()
	if err != nil {
		fail(w, 500, "获取镜像列表失败: "+err.Error())
		return
	}
	cs, _ := a.Docker.ListContainers(true)
	tagUse := map[string][]string{} // "repo:tag" -> 容器名列表
	for _, c := range cs {
		tagUse[c.Image] = append(tagUse[c.Image], containerName(c))
	}
	data := []map[string]any{}
	for _, img := range imgs {
		id12 := strings.TrimPrefix(img.ID, "sha256:")[:12]
		created := time.Unix(img.Created, 0).UTC().Format("2006-01-02T15:04:05.000000000Z")
		if len(img.RepoTags) == 0 {
			data = append(data, map[string]any{
				"id": id12, "imageId": id12, "fullTag": "<none>",
				"repoTags": []string{}, "created": created, "size": img.Size,
				"used": false, "containers": []any{}, "dangling": true,
			})
			continue
		}
		for _, rt := range img.RepoTags {
			cons := tagUse[rt]
			data = append(data, map[string]any{
				"id": id12, "imageId": id12, "fullTag": rt,
				"repoTags": img.RepoTags, "created": created, "size": img.Size,
				"used": len(cons) > 0, "containers": cons,
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
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("%s", truncate(string(b), 200))
	}
	return nil
}

func (a *App) imageDelete(w http.ResponseWriter, r *http.Request, id string) {
	force := r.URL.Query().Get("force") == "1"
	if err := a.removeImage(id, force); err != nil {
		fail(w, 500, "删除镜像失败: "+err.Error())
		return
	}
	a.Logs.Add("INFO", "删除镜像 "+id, "realtime")
	ok(w, nil)
}

func (a *App) imageTag(w http.ResponseWriter, r *http.Request, id string) {
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
	if err := a.tagImage(id, repo, tag); err != nil {
		fail(w, 500, err.Error())
		return
	}
	ok(w, nil)
}

// imagesPrune POST /api/images/prune[?all=1]
// 实际逻辑在 updates.go 的 PruneImages（与「自动清理镜像」共用同一套挑选规则，
// 都会保留 *-bak 回滚镜像）。
func (a *App) imagesPrune(w http.ResponseWriter, r *http.Request) {
	a.handlePruneImages(w, r)
}

// handleImagePull POST /api/images/pull {image, tag} —— 后台拉取，进度走 WS
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
	writeJSON(w, 200, map[string]any{"success": true, "message": "拉取任务已启动: " + image + ":" + tag})
}

func (a *App) pullImageStream(image, tag string) {
	// 广播本次拉取的「网络路径」：让用户明确知道走的是直连还是代理。
	// dockerd 负责真正的拉取，面板只是发起方——代理是否生效取决于 dockerd 的环境变量。
	cfg := a.readProxyConfig()
	viaProxy := cfg.Enabled && (cfg.HTTP != "" || cfg.HTTPS != "")
	pathMsg := "网络路径：宿主机 dockerd 直连（未配置代理）"
	if viaProxy {
		entry := cfg.HTTPS
		if entry == "" {
			entry = cfg.HTTP
		}
		pathMsg = "网络路径：宿主机 dockerd → 代理 " + entry
	}
	a.Hub.Broadcast("update_progress", map[string]any{
		"container": image, "message": "开始拉取 " + image + ":" + tag,
		"status": "start", "percentage": 0, "via_proxy": viaProxy,
	})
	a.Hub.Broadcast("update_progress", map[string]any{
		"container": image, "message": pathMsg,
		"status": "info", "percentage": 0, "via_proxy": viaProxy,
	})

	resp, err := a.Docker.doRaw("POST", "/images/create", url.Values{"fromImage": {image}, "tag": {tag}})
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
			"container": image, "message": strings.TrimSpace(status + " " + id + " " + progress),
			"status": status, "percentage": 0,
		})
	}
	a.Hub.Broadcast("update_progress", map[string]any{"container": image, "message": "拉取完成", "status": "success", "percentage": 100})
	a.Hub.Broadcast("containers_updated", map[string]any{})
}

// imageLoad POST /api/images/load —— multipart tar 导入
func (a *App) imageLoad(w http.ResponseWriter, r *http.Request) {
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
	req, err := http.NewRequest("POST", a.Docker.base+"/images/load", file)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/x-tar")
	rp, err := a.Docker.client.Do(req)
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
	a.Logs.Add("SUCCESS", "镜像导入完成", "realtime")
	writeJSON(w, 200, map[string]any{"success": true, "message": "镜像导入完成"})
}
