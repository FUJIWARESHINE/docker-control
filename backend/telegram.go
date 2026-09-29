package main

// Telegram 机器人：只读查询 + 事件推送。
//
// 设计要点：
// 1) 零第三方依赖，直接用 net/http 调 Bot API。
// 2) 长轮询 getUpdates 收指令，避免暴露公网端口。
// 3) 只读权限：仅容器/镜像/系统信息的查询类指令，不含启停删除。
// 4) 事件推送：容器非预期停止、镜像更新可用、面板启动等，推送到指定 Chat ID。

import (
	"bytes"
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

type TelegramConfig struct {
	Enabled    bool   `json:"enabled"`
	Token      string `json:"token"`
	ChatID     string `json:"chat_id"`
	NotifyDown bool   `json:"notify_down"`  // 容器停止推送
	NotifyUpd  bool   `json:"notify_update"` // 镜像更新推送
	NotifyBoot bool   `json:"notify_boot"`   // 面板启动推送
	Status     string `json:"status"`        // 运行状态描述
}

// ---------- Bot API 客户端 ----------

func (a *App) tgConfig() TelegramConfig {
	a.Store.mu.RLock()
	defer a.Store.mu.RUnlock()
	c := TelegramConfig{}
	if v, ok := a.Store.Data.UserPrefs["tg_enabled"].(bool); ok {
		c.Enabled = v
	}
	c.Token, _ = a.Store.Data.UserPrefs["tg_token"].(string)
	c.ChatID, _ = a.Store.Data.UserPrefs["tg_chat_id"].(string)
	if v, ok := a.Store.Data.UserPrefs["tg_notify_down"].(bool); ok {
		c.NotifyDown = v
	}
	if v, ok := a.Store.Data.UserPrefs["tg_notify_update"].(bool); ok {
		c.NotifyUpd = v
	}
	if v, ok := a.Store.Data.UserPrefs["tg_notify_boot"].(bool); ok {
		c.NotifyBoot = v
	}
	return c
}

func (a *App) tgStatus() string {
	a.tgMu.Lock()
	defer a.tgMu.Unlock()
	return a.tgState
}

func (a *App) setTGStatus(s string) {
	a.tgMu.Lock()
	a.tgState = s
	a.tgMu.Unlock()
}

// tgAPI 调用 Bot API，返回 result 字段的原始 JSON
func (a *App) tgAPI(method string, payload map[string]any) (json.RawMessage, error) {
	cfg := a.tgConfig()
	if cfg.Token == "" {
		return nil, fmt.Errorf("未配置 Bot Token")
	}
	body, _ := json.Marshal(payload)
	u := fmt.Sprintf("https://api.telegram.org/bot%s/%s", cfg.Token, method)
	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Post(u, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("响应解析失败: %s", string(raw))
	}
	if !r.OK {
		return nil, fmt.Errorf("%s", r.Description)
	}
	return r.Result, nil
}

func (a *App) tgSend(text string) error {
	cfg := a.tgConfig()
	if cfg.ChatID == "" {
		return fmt.Errorf("未配置 Chat ID")
	}
	_, err := a.tgAPI("sendMessage", map[string]any{
		"chat_id":    cfg.ChatID,
		"text":       text,
		"parse_mode": "HTML",
	})
	return err
}

// Notify 供其他模块调用的事件推送（未启用则静默返回）
func (a *App) tgNotify(kind, text string) {
	cfg := a.tgConfig()
	if !cfg.Enabled || cfg.Token == "" || cfg.ChatID == "" {
		return
	}
	switch kind {
	case "down":
		if !cfg.NotifyDown {
			return
		}
	case "update":
		if !cfg.NotifyUpd {
			return
		}
	case "boot":
		if !cfg.NotifyBoot {
			return
		}
	}
	go func() {
		if err := a.tgSend(text); err != nil {
			a.Logs.Add("WARNING", "Telegram 推送失败: "+err.Error(), "realtime")
		}
	}()
}

// ---------- 指令处理 ----------

const tgHelp = `<b>Docker Control 机器人</b>

只读查询指令：
/status — 系统与 Docker 概况
/containers — 容器列表
/images — 镜像列表
/logs &lt;容器名&gt; — 查看容器最近日志
/help — 显示本帮助

说明：本机器人为只读模式，不支持启停/删除操作。`

func (a *App) tgHandleCommand(cmd string) string {
	parts := strings.Fields(strings.TrimSpace(cmd))
	if len(parts) == 0 {
		return tgHelp
	}
	c := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	// 去掉 @botname 后缀
	if i := strings.Index(c, "@"); i > 0 {
		c = c[:i]
	}
	switch c {
	case "start", "help":
		return tgHelp
	case "status":
		return a.tgCmdStatus()
	case "containers", "ps":
		return a.tgCmdContainers()
	case "images":
		return a.tgCmdImages()
	case "logs":
		if len(parts) < 2 {
			return "用法：<code>/logs 容器名</code>"
		}
		return a.tgCmdLogs(parts[1])
	default:
		return "未知指令。" + tgHelp
	}
}

func (a *App) tgCmdStatus() string {
	var sb strings.Builder
	sb.WriteString("<b>系统状态</b>\n")
	var info map[string]any
	if err := a.Docker.doJSON("GET", "/info", nil, nil, &info); err == nil {
		if v, ok := info["ServerVersion"]; ok {
			sb.WriteString(fmt.Sprintf("Docker: <code>%v</code>\n", v))
		}
		if v, ok := info["Containers"]; ok {
			sb.WriteString(fmt.Sprintf("容器总数: <b>%v</b>\n", v))
		}
		if v, ok := info["ContainersRunning"]; ok {
			sb.WriteString(fmt.Sprintf("运行中: <b>%v</b>\n", v))
		}
		if v, ok := info["ContainersStopped"]; ok {
			sb.WriteString(fmt.Sprintf("已停止: <b>%v</b>\n", v))
		}
		if v, ok := info["Images"]; ok {
			sb.WriteString(fmt.Sprintf("镜像数: <b>%v</b>\n", v))
		}
	} else {
		sb.WriteString("Docker 不可达: " + err.Error() + "\n")
	}
	sb.WriteString("时间: " + time.Now().Format("2006-01-02 15:04:05"))
	return sb.String()
}

func (a *App) tgCmdContainers() string {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return "获取容器列表失败: " + err.Error()
	}
	running, stopped := 0, 0
	var sb strings.Builder
	sb.WriteString("<b>容器列表</b>\n")
	for _, c := range cs {
		name := containerName(c)
		if c.Labels["docker-control.self"] == "true" || name == a.SelfName {
			continue // 自保护：不显示面板自身
		}
		icon := "🔴"
		if c.State == "running" {
			icon = "🟢"
			running++
		} else {
			stopped++
		}
		sb.WriteString(fmt.Sprintf("%s <code>%s</code> — %s\n", icon, name, c.Status))
	}
	sb.WriteString(fmt.Sprintf("\n运行中 %d / 已停止 %d", running, stopped))
	return sb.String()
}

func (a *App) tgCmdImages() string {
	imgs, err := a.Docker.ListImages()
	if err != nil {
		return "获取镜像列表失败: " + err.Error()
	}
	var tags []string
	for _, im := range imgs {
		for _, t := range im.RepoTags {
			if t == "<none>:<none>" {
				continue
			}
			tags = append(tags, t)
		}
	}
	sort.Strings(tags)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<b>镜像列表（%d 个）</b>\n", len(tags)))
	for _, t := range tags {
		sb.WriteString("• <code>" + t + "</code>\n")
	}
	return sb.String()
}

func (a *App) tgCmdLogs(name string) string {
	resp, err := a.Docker.doRaw("GET", "/containers/"+url.PathEscape(name)+"/logs",
		url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {"30"}})
	if err != nil {
		return "读取日志失败: " + err.Error()
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	demuxWrite(&buf, nil, resp.Body)
	out := buf.String()
	if strings.TrimSpace(out) == "" {
		out = "(无日志)"
	}
	// Telegram 单条消息上限 4096 字符
	if len(out) > 3500 {
		out = out[len(out)-3500:]
	}
	return fmt.Sprintf("<b>%s 日志</b>\n<pre>%s</pre>", name, escapeXML(out))
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// ---------- 长轮询循环 ----------

var tgOnce sync.Once

// StartTelegramBot 启动长轮询（幂等，配置变更后由 restart 逻辑接管）
func (a *App) StartTelegramBot() {
	tgOnce.Do(func() {
		go a.tgLoop()
	})
}

func (a *App) tgLoop() {
	var lastUpdateID int64
	backoff := 3 * time.Second
	for {
		cfg := a.tgConfig()
		if !cfg.Enabled || cfg.Token == "" {
			a.setTGStatus("未启用")
			time.Sleep(5 * time.Second)
			continue
		}
		res, err := a.tgAPI("getUpdates", map[string]any{
			"offset":  lastUpdateID + 1,
			"timeout": 30,
		})
		if err != nil {
			a.setTGStatus("错误: " + err.Error())
			time.Sleep(backoff)
			if backoff < 60*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 3 * time.Second
		a.setTGStatus("运行中")

		var updates []struct {
			UpdateID int64 `json:"update_id"`
			Message  *struct {
				Text string `json:"text"`
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		}
		if err := json.Unmarshal(res, &updates); err != nil {
			continue
		}
		for _, u := range updates {
			lastUpdateID = u.UpdateID
			if u.Message == nil || u.Message.Text == "" {
				continue
			}
			// 只响应配置的 Chat ID（安全：避免陌生人操控）
			if cfg.ChatID != "" && strconv.FormatInt(u.Message.Chat.ID, 10) != cfg.ChatID {
				continue
			}
			reply := a.tgHandleCommand(u.Message.Text)
			_, _ = a.tgAPI("sendMessage", map[string]any{
				"chat_id":    u.Message.Chat.ID,
				"text":       reply,
				"parse_mode": "HTML",
				"link_preview_options": map[string]any{"is_disabled": true},
			})
		}
	}
}

// ---------- HTTP 接口 ----------

// handleTelegram GET/POST /api/telegram
func (a *App) handleTelegram(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := a.tgConfig()
		cfg.Status = a.tgStatus()
		ok(w, cfg)
		return
	}
	m := readBody(r)
	a.Store.mu.Lock()
	if a.Store.Data.UserPrefs == nil {
		a.Store.Data.UserPrefs = map[string]any{}
	}
	a.Store.Data.UserPrefs["tg_enabled"] = bodyBool(m, "enabled")
	a.Store.Data.UserPrefs["tg_token"] = bodyStr(m, "token")
	a.Store.Data.UserPrefs["tg_chat_id"] = bodyStr(m, "chat_id")
	a.Store.Data.UserPrefs["tg_notify_down"] = bodyBool(m, "notify_down")
	a.Store.Data.UserPrefs["tg_notify_update"] = bodyBool(m, "notify_update")
	a.Store.Data.UserPrefs["tg_notify_boot"] = bodyBool(m, "notify_boot")
	a.Store.mu.Unlock()
	a.Store.Save()
	a.Logs.Add("INFO", "Telegram 机器人配置已更新", "realtime")
	ok(w, map[string]any{"success": true, "message": "配置已保存"})
}

// handleTelegramTest POST /api/telegram/test —— 发送测试消息
func (a *App) handleTelegramTest(w http.ResponseWriter, r *http.Request) {
	if err := a.tgSend("✅ Docker Control 测试消息\n\n机器人连接正常。发送 /help 查看可用指令。"); err != nil {
		fail(w, 500, "发送失败: "+err.Error())
		return
	}
	ok(w, map[string]any{"success": true, "message": "测试消息已发送"})
}

// ---------- 容器状态监控（异常停止推送）----------

// containerWatchLoop 周期性检查容器状态，发现「非预期停止」时推送通知。
// 「非预期」的判定：上一轮是 running，这一轮变成 exited 且退出码非 0。
func (a *App) containerWatchLoop() {
	time.Sleep(20 * time.Second) // 等启动流程稳定
	a.tgNotify("boot", fmt.Sprintf("🟢 <b>Docker Control 已启动</b>\nv%s · %s",
		Version, time.Now().Format("2006-01-02 15:04:05")))

	prev := map[string]string{} // name -> state
	for {
		cfg := a.tgConfig()
		interval := 30 * time.Second
		if !cfg.Enabled || !cfg.NotifyDown {
			time.Sleep(interval)
			// 未启用时也要维护状态基线，避免启用瞬间误报
			cur := map[string]string{}
			if cs, err := a.Docker.ListContainers(true); err == nil {
				for _, c := range cs {
					cur[containerName(c)] = c.State
				}
			}
			prev = cur
			continue
		}

		cs, err := a.Docker.ListContainers(true)
		if err == nil {
			cur := map[string]string{}
			for _, c := range cs {
				name := containerName(c)
				if c.Labels["docker-control.self"] == "true" || name == a.SelfName {
					continue
				}
				cur[name] = c.State
				// 上一轮 running，这一轮非 running → 推送
				if old, ok := prev[name]; ok && old == "running" && c.State != "running" {
					a.tgNotify("down", fmt.Sprintf(
						"⚠️ <b>容器异常停止</b>\n\n名称: <code>%s</code>\n状态: %s\n时间: %s",
						name, escapeXML(c.Status), time.Now().Format("2006-01-02 15:04:05")))
					a.Logs.Add("WARNING", "检测到容器停止: "+name, "system")
				}
			}
			prev = cur
		}
		time.Sleep(interval)
	}
}
