package main

// Telegram 机器人命令与按钮分发的单元测试。
// 不依赖真实 Telegram API：只验证「输入 → (文本, 键盘)」的纯逻辑。
//
// 运行：go test -run TestTG -v

import (
	"encoding/json"
	"strings"
	"testing"
)

// newTestApp 构造一个最小可用的 App。
// Docker 指向一个必定连不上的地址，使各查询分支走「失败」路径而不 panic——
// 本测试只关心「是否被正确分发」，不关心 Docker 真实返回。
func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	return &App{
		Store:    NewStore(dir + "/config.json"),
		Docker:   NewDockerClient("unix:///nonexistent-docker.sock"),
		Hub:      NewHub(),
		Logs:     NewLogRing(50),
		Tasks:    NewTaskManager(),
		SelfName: "docker-control",
	}
}

// TestTGMainMenu 验证 /start 弹出主菜单，且按钮布局对齐设计（两列 + 末行独占）
func TestTGMainMenu(t *testing.T) {
	a := newTestApp(t)
	text, kb := a.tgHandleCommand("/start")
	if !strings.Contains(text, "Docker Control") {
		t.Errorf("欢迎语应包含产品名，实际: %s", text)
	}
	if len(kb) == 0 {
		t.Fatal("主菜单应有按钮")
	}
	// 逐行检查按钮数量：前 3 行各 2 个，最后 1 行 1 个
	for i, row := range kb {
		want := 2
		if i == len(kb)-1 {
			want = 1
		}
		if len(row) != want {
			t.Errorf("第 %d 行按钮数 = %d，期望 %d", i, len(row), want)
		}
	}
	// 校验 callback_data 唯一
	seen := map[string]bool{}
	for _, row := range kb {
		for _, b := range row {
			d, _ := b["callback_data"].(string)
			if d == "" {
				t.Error("按钮缺少 callback_data")
			}
			if seen[d] {
				t.Errorf("callback_data 重复: %s", d)
			}
			seen[d] = true
		}
	}
}

// TestTGCallbackData 校验所有按钮的 callback_data 都能被分发（不返回"未知操作"）
func TestTGCallbackData(t *testing.T) {
	a := newTestApp(t)
	// 收集主菜单 + 更多菜单的所有按钮
	all := [][]map[string]any{}
	all = append(all, kbMain()...)
	all = append(all, kbMore()...)
	all = append(all, kbBack()...)

	// updates 分支会真实访问 registry 拉取 digest，跳过以免测试依赖外网
	skip := map[string]bool{"updates": true}

	for _, row := range all {
		for _, b := range row {
			data, _ := b["callback_data"].(string)
			if skip[data] {
				continue
			}
			res, kb := a.tgHandleCallback(data)
			if res == "" {
				t.Errorf("callback_data=%q 返回空文本", data)
			}
			if kb == nil {
				t.Errorf("callback_data=%q 返回 nil 键盘", data)
			}
		}
	}
}

// TestTGCallbackMaxLen 校验 callback_data 不超过 Telegram 的 64 字节上限。
// 容器名可能非常长（compose 生成的形如 project_service_1_replica），
// 因此按钮里必须使用短引用而非原始名字。
func TestTGCallbackMaxLen(t *testing.T) {
	// 模拟超长容器名
	long := strings.Repeat("very-long-container-name_", 10)
	ref := tgMakeRef(long)
	if len(ref) > 3 {
		t.Errorf("短引用应 ≤3 字符，实际 %d: %s", len(ref), ref)
	}
	if tgResolveRef(ref) != long {
		t.Error("短引用应能还原原始容器名")
	}
	// 用短引用构造的所有 callback_data 都必须在 64 字节内
	for _, act := range []string{"menu", "logs", "restart", "ok", "stop"} {
		data := "c:" + act + ":" + ref
		if len(data) > 64 {
			t.Errorf("callback_data 超长(%d): %s", len(data), data)
		}
	}
	// 同一容器多次取引用应稳定（复用）
	if tgMakeRef(long) != ref {
		t.Error("同一容器名应复用同一个短引用")
	}
}

// TestTGRefIsolation 不同容器应获得不同短引用
func TestTGRefIsolation(t *testing.T) {
	a1 := tgMakeRef("alpha-unique-1")
	b1 := tgMakeRef("beta-unique-2")
	if a1 == b1 {
		t.Errorf("不同容器不应共用引用: %s", a1)
	}
	if tgResolveRef(a1) != "alpha-unique-1" || tgResolveRef(b1) != "beta-unique-2" {
		t.Error("引用还原错误")
	}
	// 未登记的引用原样返回（兼容直接传名字）
	if tgResolveRef("raw-name") != "raw-name" {
		t.Error("未登记引用应原样返回")
	}
}

// TestTGTextCommands 验证文本命令与按钮等价
func TestTGTextCommands(t *testing.T) {
	a := newTestApp(t)
	cases := []string{"start", "help", "menu", "about", "version", "proxy", "/start", "/help"}
	for _, c := range cases {
		text, kb := a.tgHandleCommand(c)
		if text == "" {
			t.Errorf("命令 %q 返回空文本", c)
		}
		if kb == nil {
			t.Errorf("命令 %q 返回 nil 键盘", c)
		}
	}
	// 未知命令应回主菜单而非报错崩溃
	text, kb := a.tgHandleCommand("/nonexistent")
	if !strings.Contains(text, "未知指令") {
		t.Errorf("未知命令应提示，实际: %s", text)
	}
	if len(kb) == 0 {
		t.Error("未知命令应仍返回主菜单")
	}
}

// TestTGLogsUsage 验证 /logs 缺少参数时给出用法提示
func TestTGLogsUsage(t *testing.T) {
	a := newTestApp(t)
	text, _ := a.tgHandleCommand("/logs")
	if !strings.Contains(text, "用法") {
		t.Errorf("/logs 无参数应提示用法，实际: %s", text)
	}
}

// TestTGButtonJSON Telegram 要求按钮结构为 {"text":..., "callback_data":...}
func TestTGButtonJSON(t *testing.T) {
	kbs := [][][]map[string]any{kbMain(), kbMore(), kbBack()}
	for _, kb := range kbs {
		b, err := json.Marshal(map[string]any{"inline_keyboard": kb})
		if err != nil {
			t.Fatalf("按钮序列化失败: %v", err)
		}
		if !strings.Contains(string(b), "inline_keyboard") {
			t.Error("键盘缺 inline_keyboard 包装")
		}
		if !strings.Contains(string(b), "callback_data") {
			t.Error("按钮缺 callback_data 字段")
		}
	}
}

// TestTGAllowedChat 白名单校验
func TestTGAllowedChat(t *testing.T) {
	a := newTestApp(t)
	cfg := TelegramConfig{ChatID: "123456"}
	if !a.tgAllowedChat(cfg, 123456) {
		t.Error("配置内的 Chat ID 应放行")
	}
	if a.tgAllowedChat(cfg, 999999) {
		t.Error("非配置 Chat ID 应拒绝")
	}
	// 未配置 Chat ID 时应全部拒绝（安全默认）
	if a.tgAllowedChat(TelegramConfig{}, 123456) {
		t.Error("未配置 Chat ID 时应拒绝所有消息")
	}
}

// TestShellQuote 校验命令注入防护
func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/etc/foo":            "'/etc/foo'",
		"/tmp/a'b":            `'/tmp/a'\''b'`,
		"/tmp/; rm -rf /":     "'/tmp/; rm -rf /'",
		"/tmp/$(whoami)":      "'/tmp/$(whoami)'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestMergeNoProxy 校验 NO_PROXY 合并逻辑
func TestMergeNoProxy(t *testing.T) {
	out := mergeNoProxy("example.com,localhost")
	if !strings.Contains(out, "example.com") {
		t.Error("应保留用户填写项")
	}
	if !strings.Contains(out, "docker.m.daocloud.io") {
		t.Error("应自动附加镜像加速器")
	}
	// 去重：localhost 在用户项和默认项都出现，只应出现一次
	if strings.Count(out, "localhost") != 1 {
		t.Errorf("localhost 应去重，实际: %s", out)
	}
	// 空输入也要有默认白名单
	if mergeNoProxy("") == "" {
		t.Error("空输入应返回默认白名单")
	}
}

// TestAllowedComposePath 校验 compose 目录白名单
func TestAllowedComposePath(t *testing.T) {
	// 未配置 COMPOSE_DIRS 时放行一切（向后兼容）
	// 配置后只允许白名单内路径 —— 通过环境变量注入
	t.Setenv("COMPOSE_DIRS", "/srv/docker")
	if !allowedComposePath("/srv/docker/app/docker-compose.yml") {
		t.Error("白名单内路径应放行")
	}
	if allowedComposePath("/etc/passwd") {
		t.Error("白名单外路径应拒绝")
	}
	// 路径穿越攻击
	if allowedComposePath("/srv/docker/../../etc/passwd") {
		t.Error("路径穿越应被拒绝")
	}
	// 前缀相似但不是子目录
	if allowedComposePath("/srv/docker-evil/x.yml") {
		t.Error("前缀相似目录不应放行")
	}
	// 白名单目录本身
	if !allowedComposePath("/srv/docker") {
		t.Error("白名单目录自身应放行")
	}
}
