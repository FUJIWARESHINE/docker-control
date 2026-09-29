package main

// Telegram 机器人：按钮式交互 + 事件推送。
//
// 设计要点：
// 1) 零第三方依赖，直接用 net/http 调 Bot API。
// 2) 长轮询 getUpdates 收消息与按钮回调，无需暴露公网端口。
// 3) 交互形态：Inline Keyboard 按钮菜单（对齐 Diancup 的交互习惯），
//    同时保留文本命令作为快捷方式。
// 4) 权限：查询类操作开放；写操作（重启服务/重启容器）需二次确认。
// 5) 事件推送：容器非预期停止、面板启动等，推送到指定 Chat ID。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
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
	NotifyDown bool   `json:"notify_down"`   // 容器停止推送
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

// tgPoll 长轮询专用的 Bot API 调用。
// 与 tgAPI 的区别：HTTP 超时更长（Telegram 侧 timeout 25s + 15s 余量），
// 且只重试 1 次（长轮询本身失败后由主循环退避重来，不必在此堆叠）。
func (a *App) tgPoll(method string, payload map[string]any) (json.RawMessage, error) {
	cfg := a.tgConfig()
	if cfg.Token == "" {
		return nil, fmt.Errorf("未配置 Bot Token")
	}
	body, _ := json.Marshal(payload)
	u := fmt.Sprintf("https://api.telegram.org/bot%s/%s", cfg.Token, method)

	cli := tgHTTPClient()
	cli.Timeout = 45 * time.Second

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

// tgDialer 返回 Telegram 请求专用的 Dialer。
//
// 背景：部分网络环境存在 DNS 污染（把 api.telegram.org 解析到无关 IP，
// 如 Facebook 的 31.13.x.x / 2a03:2880::face:b00c），表现为间歇性
// TLS handshake failure。此时可通过环境变量指定可信 DNS 覆盖解析：
//
//	TG_DNS_ADDR=8.8.8.8:53        # 用该 DNS 解析（推荐 DoH 不可用时用这个）
//	TG_FORCE_IP=149.154.167.220   # 直接写死 IP（绕过 DNS，最粗暴但最可靠）
//
// 未设置时返回 nil，调用方走 Go 默认解析，行为与从前一致。
func tgDialer() (*net.Dialer, string) {
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}

	// 优先级 1：直接写死 IP（最可靠）
	if ip := strings.TrimSpace(os.Getenv("TG_FORCE_IP")); ip != "" {
		return d, ip
	}
	// 优先级 2：指定 DNS 解析器
	if addr := strings.TrimSpace(os.Getenv("TG_DNS_ADDR")); addr != "" {
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				nd := net.Dialer{Timeout: 5 * time.Second}
				return nd.DialContext(ctx, "udp", addr)
			},
		}
		d.Resolver = r
	}
	return d, ""
}

// tgHTTPClient 构造带重试友好参数的 Telegram 客户端
func tgHTTPClient() *http.Client {
	d, forceIP := tgDialer()
	tr := &http.Transport{
		DialContext:         d.DialContext,
		TLSHandshakeTimeout: 15 * time.Second,
		// 强制走 IPv4：污染结果常是 IPv6/无效 IP
		ForceAttemptHTTP2: true,
	}
	if forceIP != "" {
		// 把 Telegram 域名固定解析到指定 IP
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if strings.Contains(addr, "api.telegram.org") {
				addr = net.JoinHostPort(forceIP, "443")
			}
			return d.DialContext(ctx, network, addr)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: tr}
}

// tgAPI 调用 Bot API，返回 result 字段的原始 JSON。
//
// 网络层自动重试（最多 3 次，指数退避）：
// Telegram 域名常被解析到多个 IP，其中部分不可达，会出现间歇性的
// TLS handshake failure / i/o timeout。这种错误重试一次通常即成功。
// 注意：只对「网络类错误」重试，Telegram 返回的业务错误（如 Token 无效）
// 不重试，避免无谓等待。
func (a *App) tgAPI(method string, payload map[string]any) (json.RawMessage, error) {
	cfg := a.tgConfig()
	if cfg.Token == "" {
		return nil, fmt.Errorf("未配置 Bot Token")
	}
	body, _ := json.Marshal(payload)
	u := fmt.Sprintf("https://api.telegram.org/bot%s/%s", cfg.Token, method)

	var lastErr error
	backoff := 800 * time.Millisecond
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			time.Sleep(backoff)
			backoff *= 3
		}
		resp, err := tgHTTPClient().Post(u, "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue // 网络类错误 → 重试
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var r struct {
			OK          bool            `json:"ok"`
			Result      json.RawMessage `json:"result"`
			Description string          `json:"description"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			lastErr = fmt.Errorf("响应解析失败: %s", string(raw))
			continue
		}
		if !r.OK {
			// 业务错误（Token 无效、chat 不存在等）重试无意义，直接返回
			return nil, fmt.Errorf("%s", r.Description)
		}
		return r.Result, nil
	}
	return nil, fmt.Errorf("重试 3 次仍失败: %w", lastErr)
}

// tgSend 向配置的 Chat ID 发送消息
func (a *App) tgSend(text string) error {
	cfg := a.tgConfig()
	if cfg.ChatID == "" {
		return fmt.Errorf("未配置 Chat ID")
	}
	_, err := a.tgAPI("sendMessage", map[string]any{
		"chat_id":    cfg.ChatID,
		"text":       text,
		"parse_mode": "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	})
	return err
}

// tgSendKB 发送带 Inline Keyboard 的消息
func (a *App) tgSendKB(chatID int64, text string, kb [][]map[string]any) error {
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	}
	if len(kb) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": kb}
	}
	_, err := a.tgAPI("sendMessage", payload)
	return err
}

// tgEditKB 原地替换消息内容（用于按钮翻页/返回，避免刷屏）
func (a *App) tgEditKB(chatID, msgID int64, text string, kb [][]map[string]any) error {
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": msgID,
		"text":       text,
		"parse_mode": "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	}
	if len(kb) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": kb}
	} else {
		payload["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]any{}}
	}
	_, err := a.tgAPI("editMessageText", payload)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		// 用户重复点同一按钮（如连点两次「检查更新」），内容与键盘都没变，
		// Telegram 会拒绝这次编辑。这不算错误，但用户也看不到任何反馈，
		// 容易误以为按钮失灵。
		//
		// 解法：在末尾追加一行零宽字符组成的时间戳（肉眼不可见，但让
		// Telegram 认为内容已变化），用户便能感知到「刷新了」。
		payload["text"] = text + "\n" + zeroWidthStamp()
		if _, err2 := a.tgAPI("editMessageText", payload); err2 != nil {
			// 仍失败就静默放过：内容本来就完全一致，用户看到的就是正确结果
			return nil
		}
	}
	return nil
}

// zeroWidthStamp 返回由零宽字符编码的时间戳，肉眼完全不可见。
// 用零宽空格(U+200B)与零宽不连字(U+200C)表示二进制位。
func zeroWidthStamp() string {
	n := time.Now().UnixNano()
	var sb strings.Builder
	for i := 0; i < 26; i++ {
		if n&1 == 1 {
			sb.WriteRune('\u200B') // 1
		} else {
			sb.WriteRune('\u200C') // 0
		}
		n >>= 1
	}
	return sb.String()
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

// ---------- 按钮菜单 ----------

// 按钮回调数据约定（callback_data 上限 64 字节）：
//
//	menu            回到主菜单
//	status          系统状态
//	containers      容器列表
//	projects        Compose 项目列表
//	images          镜像列表
//	updates         检查镜像更新
//	version         版本信息
//	about           关于
//	more            更多功能
//	px              代理状态
//	svc:restart     重启服务（需二次确认）
//	svc:ok          确认重启
//	c:menu:<ref>    容器详情（ref 为短引用，见 tgContainerRef）
//	c:logs:<ref>    查看容器日志
//	c:restart:<ref> 重启容器（需二次确认）
//	c:ok:<ref>      确认重启容器
//	c:stop:<ref>    停止容器
//
// 注意：容器名可能超过 Telegram callback_data 的 64 字节上限，
// 因此统一用「短引用」传递——短引用是容器名的稳定映射，见 tgRefMap。

// tgRefMap 维护「短引用 → 容器名」的映射。
// 每次渲染容器列表时重建，避免容器名过长撑爆 callback_data。
var (
	tgRefMu   sync.Mutex
	tgRefMap  = map[string]string{}
	tgRefSeq  int
)

// tgMakeRef 为容器名分配（或复用）一个短引用，形如 r1、r2，长度恒 ≤ 3 字符。
func tgMakeRef(name string) string {
	tgRefMu.Lock()
	defer tgRefMu.Unlock()
	// 已存在则复用
	for r, n := range tgRefMap {
		if n == name {
			return r
		}
	}
	tgRefSeq++
	r := "r" + strconv.Itoa(tgRefSeq)
	tgRefMap[r] = name
	return r
}

// tgResolveRef 把短引用还原为容器名。找不到时原样返回（兼容直接传容器名的旧路径）。
func tgResolveRef(ref string) string {
	tgRefMu.Lock()
	defer tgRefMu.Unlock()
	if n, ok := tgRefMap[ref]; ok {
		return n
	}
	return ref
}

// btn 构造一个按钮
func btn(text, data string) map[string]any {
	return map[string]any{"text": text, "callback_data": data}
}

// kbMain 主菜单（布局对齐 Diancup：两列按钮 + 末行独占）
func kbMain() [][]map[string]any {
	return [][]map[string]any{
		{btn("🔄 检查更新", "updates"), btn("📋 项目列表", "projects")},
		{btn("📦 容器列表", "containers"), btn("🖥 系统状态", "status")},
		{btn("🔖 版本检查", "version"), btn("🧩 镜像列表", "images")},
		{btn("⚙️ 更多功能", "more")},
	}
}

// kbMore 更多功能（容纳镜像/代理/关于等次级入口）
func kbMore() [][]map[string]any {
	return [][]map[string]any{
		{btn("🌐 代理状态", "px"), btn("ℹ️ 关于", "about")},
		{btn("◀️ 返回主菜单", "menu")},
	}
}

// kbBack 只有返回按钮
func kbBack() [][]map[string]any {
	return [][]map[string]any{{btn("◀️ 返回主菜单", "menu")}}
}

const tgWelcome = `<b>Docker Control</b>
Docker 容器管理 · 宿主机监控

请选择操作：`

const tgAbout = `<b>关于 Docker Control</b>

纯 Go 实现的轻量 Docker 管理面板：
· 零第三方面板依赖，直连 Docker Engine API
· 容器 / 镜像 / 网络 / 卷 / Compose 全功能管理
· 镜像拉取代理（改 dockerd 全局代理）
· Telegram 机器人（按钮式交互）

面板自身以 docker-control.self=true 标记做自保护，
机器人不会展示或操作面板容器本身。`

// ---------- 回调分发 ----------

// tgHandleCallback 处理按钮点击，返回 (新文本, 新键盘)
func (a *App) tgHandleCallback(data string) (string, [][]map[string]any) {
	switch data {
	case "menu":
		return tgWelcome, kbMain()
	case "status":
		return a.tgCmdStatus(), kbBack()
	case "containers":
		return a.tgCmdContainers(), a.kbContainers()
	case "projects":
		return a.tgCmdProjects(), kbBack()
	case "images":
		return a.tgCmdImages(), kbBack()
	case "updates":
		return a.tgCmdUpdates(), kbBack()
	case "version":
		return a.tgCmdVersion(), kbBack()
	case "about":
		return tgAbout, kbBack()
	case "px":
		return a.tgCmdProxy(), kbBack()
	case "more":
		return "<b>⚙️ 更多功能</b>\n\n请选择操作：", kbMore()

	case "svc":
		return "<b>⚙️ 服务操作</b>\n\n写操作需二次确认：",
			[][]map[string]any{
				{btn("♻️ 重启 Docker 服务", "svc:restart")},
				{btn("◀️ 返回", "more")},
			}
	case "svc:restart":
		return "<b>⚠️ 确认重启 Docker 服务？</b>\n\n所有容器会重启，restart 策略为 always 的会自动恢复。",
			[][]map[string]any{
				{btn("✅ 确认重启", "svc:ok"), btn("❌ 取消", "menu")},
			}
	case "svc:ok":
		go a.restartDockerService()
		return "🔄 <b>已提交重启指令</b>\n\nDocker 服务正在重启，约 10 秒后恢复。", kbBack()
	}

	// 容器相关：c:<action>:<ref>
	// ref 是容器名的短引用（见 tgMakeRef），避免 callback_data 超过 64 字节
	if strings.HasPrefix(data, "c:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "c:"), ":", 2)
		action := parts[0]
		ref := ""
		if len(parts) > 1 {
			ref = parts[1]
		}
		name := tgResolveRef(ref)
		switch action {
		case "menu":
			return a.tgContainerDetail(name), [][]map[string]any{
				{btn("📜 查看日志", "c:logs:"+ref), btn("🔄 重启", "c:restart:"+ref)},
				{btn("⏹ 停止", "c:stop:"+ref)},
				{btn("◀️ 容器列表", "containers")},
			}
		case "logs":
			return a.tgCmdLogs(name), [][]map[string]any{
				{btn("🔄 刷新", "c:logs:"+ref), btn("◀️ 返回", "c:menu:"+ref)},
			}
		case "restart":
			return fmt.Sprintf("<b>⚠️ 确认重启容器 <code>%s</code>？</b>", escapeXML(name)),
				[][]map[string]any{
					{btn("✅ 确认", "c:ok:"+ref), btn("❌ 取消", "c:menu:"+ref)},
				}
		case "ok":
			if err := a.Docker.ContainerAction(name, "restart", 0); err != nil {
				return "❌ 重启失败：" + escapeXML(err.Error()), kbBack()
			}
			a.Logs.Add("INFO", "通过 Telegram 重启容器: "+name, "realtime")
			return fmt.Sprintf("✅ 容器 <code>%s</code> 已重启", escapeXML(name)), kbBack()
		case "stop":
			if err := a.Docker.ContainerAction(name, "stop", 10); err != nil {
				return "❌ 停止失败：" + escapeXML(err.Error()), kbBack()
			}
			a.Logs.Add("INFO", "通过 Telegram 停止容器: "+name, "realtime")
			return fmt.Sprintf("⏹ 容器 <code>%s</code> 已停止", escapeXML(name)), kbBack()
		}
	}
	return "未知操作。", kbBack()
}

// tgContainerDetail 单个容器的概况
func (a *App) tgContainerDetail(name string) string {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return "读取容器信息失败：" + escapeXML(err.Error())
	}
	for _, c := range cs {
		if containerName(c) != name {
			continue
		}
		icon := "🔴"
		if c.State == "running" {
			icon = "🟢"
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("<b>%s %s</b>\n\n", icon, escapeXML(name)))
		sb.WriteString(fmt.Sprintf("镜像：<code>%s</code>\n", escapeXML(c.Image)))
		sb.WriteString(fmt.Sprintf("状态：%s\n", escapeXML(c.Status)))
		sb.WriteString(fmt.Sprintf("ID：<code>%s</code>\n", escapeXML(c.ID[:12])))
		return sb.String()
	}
	return "容器不存在：" + escapeXML(name)
}

// restartDockerService 通过宿主机执行器重启 docker 服务
func (a *App) restartDockerService() {
	out, err := a.execOnHost("systemctl --no-block daemon-reload && systemctl --no-block restart docker")
	if err != nil {
		a.Logs.Add("WARNING", "Telegram 触发重启失败: "+err.Error(), "realtime")
		return
	}
	if out != "" {
		a.Logs.Add("INFO", "Telegram 触发重启 docker: "+out, "realtime")
	}
}

// ---------- 各页面渲染 ----------

func (a *App) tgCmdStatus() string {
	var sb strings.Builder
	sb.WriteString("<b>🖥 系统状态</b>\n\n")
	var info map[string]any
	if err := a.Docker.doJSON("GET", "/info", nil, nil, &info); err == nil {
		if v, ok := info["ServerVersion"]; ok {
			sb.WriteString(fmt.Sprintf("Docker 版本：<code>%v</code>\n", v))
		}
		if v, ok := info["OperatingSystem"]; ok {
			sb.WriteString(fmt.Sprintf("系统：<code>%v</code>\n", v))
		}
		if v, ok := info["NCPU"]; ok {
			sb.WriteString(fmt.Sprintf("CPU：<b>%v</b> 核\n", v))
		}
		if v, ok := info["MemTotal"]; ok {
			if n, ok := v.(float64); ok {
				sb.WriteString(fmt.Sprintf("内存：<b>%.1f</b> GB\n", n/1024/1024/1024))
			}
		}
		sb.WriteString("\n<b>容器</b>\n")
		if v, ok := info["Containers"]; ok {
			sb.WriteString(fmt.Sprintf("总数：<b>%v</b>\n", v))
		}
		if v, ok := info["ContainersRunning"]; ok {
			sb.WriteString(fmt.Sprintf("运行中：<b>%v</b>\n", v))
		}
		if v, ok := info["ContainersStopped"]; ok {
			sb.WriteString(fmt.Sprintf("已停止：<b>%v</b>\n", v))
		}
		if v, ok := info["Images"]; ok {
			sb.WriteString(fmt.Sprintf("\n镜像数：<b>%v</b>\n", v))
		}
	} else {
		sb.WriteString("❌ Docker 不可达：" + escapeXML(err.Error()) + "\n")
	}
	sb.WriteString("\n<i>" + time.Now().Format("2006-01-02 15:04:05") + "</i>")
	return sb.String()
}

func (a *App) tgCmdContainers() string {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return "获取容器列表失败：" + escapeXML(err.Error())
	}
	running, stopped := 0, 0
	var sb strings.Builder
	sb.WriteString("<b>📦 容器列表</b>\n\n")
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
		sb.WriteString(fmt.Sprintf("%s <code>%s</code>\n   <i>%s</i>\n", icon, escapeXML(name), escapeXML(c.Status)))
	}
	sb.WriteString(fmt.Sprintf("\n运行中 <b>%d</b> · 已停止 <b>%d</b>", running, stopped))
	sb.WriteString("\n\n<i>发送 /logs 容器名 可查看日志</i>")
	return sb.String()
}

// kbContainers 容器列表 + 每个容器一个操作入口（最多 8 个，避免按钮过载）
// 用短引用传递容器名，确保 callback_data 不超 Telegram 的 64 字节上限。
func (a *App) kbContainers() [][]map[string]any {
	cs, err := a.Docker.ListContainers(true)
	if err != nil {
		return kbBack()
	}
	var kb [][]map[string]any
	n := 0
	for _, c := range cs {
		name := containerName(c)
		if c.Labels["docker-control.self"] == "true" || name == a.SelfName {
			continue
		}
		if n >= 8 {
			break
		}
		icon := "🔴"
		if c.State == "running" {
			icon = "🟢"
		}
		label := fmt.Sprintf("%s %s", icon, name)
		if len([]rune(label)) > 30 {
			label = string([]rune(label)[:30])
		}
		kb = append(kb, []map[string]any{btn(label, "c:menu:"+tgMakeRef(name))})
		n++
	}
	if n == 0 {
		return kbBack()
	}
	kb = append(kb, []map[string]any{btn("◀️ 返回主菜单", "menu")})
	return kb
}

// tgCmdProjects Compose 项目列表
func (a *App) tgCmdProjects() string {
	projs := a.composeProjects()
	if len(projs) == 0 {
		return "<b>📋 项目列表</b>\n\n没有发现 Compose 项目。"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<b>📋 项目列表（%d 个）</b>\n\n", len(projs)))
	for _, p := range projs {
		name, _ := p["name"].(string)
		fn, _ := p["compose_filename"].(string)
		state, _ := p["state"].(string)
		rc, _ := p["running_count"].(int)
		cc, _ := p["container_count"].(int)
		icon := "🔴"
		if state == "running" {
			icon = "🟢"
		}
		sb.WriteString(fmt.Sprintf("%s <b>%s</b>\n", icon, escapeXML(name)))
		sb.WriteString(fmt.Sprintf("   <i>%s</i> · %d/%d 个容器\n", escapeXML(fn), rc, cc))
	}
	return sb.String()
}

func (a *App) tgCmdImages() string {
	imgs, err := a.Docker.ListImages()
	if err != nil {
		return "获取镜像列表失败：" + escapeXML(err.Error())
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
	sb.WriteString(fmt.Sprintf("<b>🧩 镜像列表（%d 个）</b>\n\n", len(tags)))
	limit := len(tags)
	if limit > 60 {
		limit = 60
	}
	for _, t := range tags[:limit] {
		sb.WriteString("• <code>" + escapeXML(t) + "</code>\n")
	}
	if len(tags) > limit {
		sb.WriteString(fmt.Sprintf("\n<i>… 另有 %d 个未显示</i>", len(tags)-limit))
	}
	return sb.String()
}

// tgCmdUpdates 检查容器镜像是否有更新
func (a *App) tgCmdUpdates() string {
	res, err := a.checkUpdates("")
	if err != nil {
		return "检查更新失败：" + escapeXML(err.Error())
	}
	if len(res) == 0 {
		return "<b>🔄 检查更新</b>\n\n没有发现容器。"
	}
	upd := 0
	var sb strings.Builder
	sb.WriteString("<b>🔄 检查更新</b>\n\n")
	for _, r := range res {
		if r.ContainerName == a.SelfName || r.ContainerName == "docker-control" {
			continue
		}
		switch {
		case r.HasUpdate:
			upd++
			sb.WriteString(fmt.Sprintf("⬆️ <code>%s</code> — 有新版本\n", escapeXML(r.ContainerName)))
		case r.Status == "up-to-date":
			sb.WriteString(fmt.Sprintf("✅ <code>%s</code> — 已是最新\n", escapeXML(r.ContainerName)))
		default:
			sb.WriteString(fmt.Sprintf("❔ <code>%s</code> — 无法判定\n", escapeXML(r.ContainerName)))
		}
	}
	sb.WriteString(fmt.Sprintf("\n发现 <b>%d</b> 个可更新", upd))
	return sb.String()
}

func (a *App) tgCmdVersion() string {
	sb := "<b>🔖 版本检查</b>\n\n"
	sb += fmt.Sprintf("面板版本：<code>%s</code>\n", Version)
	if a.Docker != nil {
		var info map[string]any
		if err := a.Docker.doJSON("GET", "/info", nil, nil, &info); err == nil {
			if v, ok := info["ServerVersion"]; ok {
				sb += fmt.Sprintf("Docker：<code>%v</code>\n", v)
			}
		}
	}
	sb += "\n<i>面板为单二进制静态编译，升级即替换镜像并重启容器。</i>"
	return sb
}

// tgCmdProxy 展示镜像拉取代理状态
func (a *App) tgCmdProxy() string {
	cfg := a.readProxyConfig()
	var sb strings.Builder
	sb.WriteString("<b>🌐 代理状态</b>\n\n")
	if !cfg.Enabled || (cfg.HTTP == "" && cfg.HTTPS == "") {
		sb.WriteString("当前<b>未配置代理</b>\n")
		sb.WriteString("镜像拉取走宿主机 dockerd 直连。\n")
		return sb.String()
	}
	sb.WriteString("<b>已配置代理</b>\n")
	if cfg.HTTP != "" {
		sb.WriteString(fmt.Sprintf("HTTP：<code>%s</code>\n", escapeXML(cfg.HTTP)))
	}
	if cfg.HTTPS != "" {
		sb.WriteString(fmt.Sprintf("HTTPS：<code>%s</code>\n", escapeXML(cfg.HTTPS)))
	}
	if cfg.NoProxy != "" {
		np := cfg.NoProxy
		if len(np) > 300 {
			np = np[:300] + "…"
		}
		sb.WriteString(fmt.Sprintf("\nNO_PROXY：<code>%s</code>\n", escapeXML(np)))
	}
	sb.WriteString("\n<i>拉取动作由宿主机 dockerd 执行，面板不经手镜像数据。</i>")
	return sb.String()
}

func (a *App) tgCmdLogs(name string) string {
	resp, err := a.Docker.doRaw("GET", "/containers/"+url.PathEscape(name)+"/logs",
		url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {"30"}})
	if err != nil {
		return "读取日志失败：" + escapeXML(err.Error())
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	demuxWrite(&buf, nil, resp.Body)
	out := buf.String()
	if strings.TrimSpace(out) == "" {
		out = "(无日志)"
	}
	// Telegram 单条消息上限 4096 字符
	if len(out) > 3000 {
		out = out[len(out)-3000:]
	}
	return fmt.Sprintf("<b>📜 %s 日志</b>\n<pre>%s</pre>", escapeXML(name), escapeXML(out))
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// tgHandleCommand 文本命令入口（保留快捷方式，与按钮菜单等价）
func (a *App) tgHandleCommand(cmd string) (string, [][]map[string]any) {
	parts := strings.Fields(strings.TrimSpace(cmd))
	if len(parts) == 0 {
		return tgWelcome, kbMain()
	}
	c := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	if i := strings.Index(c, "@"); i > 0 {
		c = c[:i]
	}
	switch c {
	case "start", "help", "menu":
		return tgWelcome, kbMain()
	case "status":
		return a.tgCmdStatus(), kbBack()
	case "containers", "ps":
		return a.tgCmdContainers(), kbBack()
	case "projects":
		return a.tgCmdProjects(), kbBack()
	case "images":
		return a.tgCmdImages(), kbBack()
	case "updates":
		return a.tgCmdUpdates(), kbBack()
	case "version":
		return a.tgCmdVersion(), kbBack()
	case "about":
		return tgAbout, kbBack()
	case "proxy":
		return a.tgCmdProxy(), kbBack()
	case "logs":
		if len(parts) < 2 {
			return "用法：<code>/logs 容器名</code>", kbBack()
		}
		return a.tgCmdLogs(parts[1]), kbBack()
	default:
		return fmt.Sprintf("未知指令 <code>%s</code>。", escapeXML(c)), kbMain()
	}
}

// ---------- 长轮询循环 ----------

var tgOnce sync.Once

// StartTelegramBot 启动长轮询（幂等）
func (a *App) StartTelegramBot() {
	tgOnce.Do(func() {
		go a.tgLoop()
	})
}

// tgUpdate 覆盖消息与按钮回调两类更新
type tgUpdate struct {
	UpdateID      int64 `json:"update_id"`
	Message       *tgMessage `json:"message"`
	CallbackQuery *struct {
		ID      string `json:"id"`
		Data    string `json:"data"`
		Message *struct {
			MessageID int64 `json:"message_id"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

type tgMessage struct {
	Text string `json:"text"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
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
		// 长轮询用独立客户端：HTTP 超时必须明显大于 Telegram 侧的 timeout，
		// 否则会出现「Telegram 还在等消息、客户端已经超时」的假失败。
		res, err := a.tgPoll("getUpdates", map[string]any{
			"offset":  lastUpdateID + 1,
			"timeout": 25,
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

		var updates []tgUpdate
		if err := json.Unmarshal(res, &updates); err != nil {
			continue
		}
		for _, u := range updates {
			lastUpdateID = u.UpdateID

			// ---- 按钮回调 ----
			if u.CallbackQuery != nil {
				cq := u.CallbackQuery
				// 立即去掉按钮上的 loading 圈
				_, _ = a.tgAPI("answerCallbackQuery", map[string]any{"callback_query_id": cq.ID})
				var chatID, msgID int64
				if cq.Message != nil {
					chatID = cq.Message.Chat.ID
					msgID = cq.Message.MessageID
				}
			if !a.tgAllowedChat(cfg, chatID) {
				a.Logs.Add("WARNING", fmt.Sprintf("Telegram 回调被拒绝（非白名单会话 %d）", chatID), "realtime")
				continue
			}
			a.Logs.Add("INFO", "Telegram 按钮点击: "+cq.Data, "realtime")
			text, kb := a.tgHandleCallback(cq.Data)
			if err := a.tgEditKB(chatID, msgID, text, kb); err != nil {
				a.Logs.Add("WARNING", "Telegram 消息更新失败: "+err.Error(), "realtime")
			}
			continue
			}

			// ---- 文本消息 ----
			if u.Message == nil || u.Message.Text == "" {
				continue
			}
			if !a.tgAllowedChat(cfg, u.Message.Chat.ID) {
				continue
			}
			a.Logs.Add("INFO", "Telegram 收到指令: "+u.Message.Text, "realtime")
			text, kb := a.tgHandleCommand(u.Message.Text)
			if err := a.tgSendKB(u.Message.Chat.ID, text, kb); err != nil {
				a.Logs.Add("WARNING", "Telegram 回复失败: "+err.Error(), "realtime")
			}
		}
	}
}

// tgAllowedChat 白名单校验：只响应配置的 Chat ID，避免陌生人操控。
// 未配置 Chat ID 时自动采用第一个来消息的会话并提示。
func (a *App) tgAllowedChat(cfg TelegramConfig, chatID int64) bool {
	if cfg.ChatID == "" {
		return false
	}
	return strconv.FormatInt(chatID, 10) == cfg.ChatID
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

// handleTelegramTest POST /api/telegram/test —— 发送带菜单的测试消息
func (a *App) handleTelegramTest(w http.ResponseWriter, r *http.Request) {
	cfg := a.tgConfig()
	if cfg.Token == "" {
		fail(w, 400, "请先填写 Bot Token 并保存")
		return
	}
	// 探测 bot 身份，顺带验证 Token 有效性
	me, err := a.tgAPI("getMe", map[string]any{})
	if err != nil {
		fail(w, 500, "Token 无效："+err.Error())
		return
	}
	var bot struct {
		Username string `json:"username"`
	}
	json.Unmarshal(me, &bot)

	chatID := cfg.ChatID
	if chatID == "" {
		fail(w, 400, "请先填写 Chat ID 并保存（可通过 @userinfobot 获取）")
		return
	}
	n, _ := strconv.ParseInt(chatID, 10, 64)
	if err := a.tgSendKB(n, tgWelcome, kbMain()); err != nil {
		fail(w, 500, "发送失败："+err.Error())
		return
	}
	ok(w, map[string]any{
		"success": true,
		"message": fmt.Sprintf("测试消息已发送（@%s）", bot.Username),
	})
}

// ---------- 容器状态监控（异常停止推送）----------

// containerWatchLoop 周期性检查容器状态，发现「非预期停止」时推送通知。
func (a *App) containerWatchLoop() {
	time.Sleep(20 * time.Second) // 等启动流程稳定
	a.tgNotify("boot", fmt.Sprintf("🟢 <b>Docker Control 已启动</b>\nv%s · %s",
		Version, time.Now().Format("2006-01-02 15:04:05")))

	prev := map[string]string{} // name -> state
	for {
		cfg := a.tgConfig()
		interval := 30 * time.Second
		needWatch := cfg.Enabled && cfg.NotifyDown

		cs, err := a.Docker.ListContainers(true)
		if err == nil {
			cur := map[string]string{}
			for _, c := range cs {
				name := containerName(c)
				if c.Labels["docker-control.self"] == "true" || name == a.SelfName {
					continue
				}
				cur[name] = c.State
				if needWatch {
					// 上一轮 running，这一轮非 running → 推送
					if old, ok := prev[name]; ok && old == "running" && c.State != "running" {
						a.tgNotify("down", fmt.Sprintf(
							"⚠️ <b>容器异常停止</b>\n\n名称：<code>%s</code>\n状态：%s\n时间：%s",
							escapeXML(name), escapeXML(c.Status), time.Now().Format("2006-01-02 15:04:05")))
						a.Logs.Add("WARNING", "检测到容器停止: "+name, "system")
					}
				}
			}
			prev = cur
		}
		time.Sleep(interval)
	}
}
