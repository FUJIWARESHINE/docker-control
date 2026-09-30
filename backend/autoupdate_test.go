package main

// 自动更新调度 / 镜像清理的回归测试。
//
// 背景：旧版调度的写法是「睡满整个间隔」，导致改设置要等一整轮才生效，
// 而且每轮都会无条件停掉容器重建 —— 哪怕仓库上根本没有新版本。
// 现在改成 30 秒 tick + NextRun 判定，并新增了「定时清理未使用镜像」。
// 本文件锁定时间解析、间隔语义、收藏状态机与清理挑选规则。

// 本文件复用 telegram_test.go 里的 newTestApp（Store 落在 t.TempDir()，
// Docker 指向不存在的 socket，因此不会真的碰容器）。

import (
	"testing"
	"time"
)

func TestParseStoreTime(t *testing.T) {
	now := time.Now()
	s := now.Format(timeLayout)
	got, ok := parseStoreTime(s)
	if !ok {
		t.Fatalf("parseStoreTime(%q) 解析失败", s)
	}
	// 秒级精度往返
	if got.Unix() != now.Unix() {
		t.Fatalf("往返时间不一致: got %v, want %v", got, now)
	}

	for _, bad := range []string{"", "2026-09-30", "2026/09/30 15:04:05", "not-a-time"} {
		if _, ok := parseStoreTime(bad); ok {
			t.Fatalf("parseStoreTime(%q) 本应失败却成功了", bad)
		}
	}
}

func TestUpdateIntervalSemantics(t *testing.T) {
	cases := []struct {
		days, hours int
		want        time.Duration
	}{
		{7, 0, 7 * 24 * time.Hour},
		{1, 0, 24 * time.Hour},
		{0, 12, 12 * time.Hour},
		{0, 1, 1 * time.Hour}, // 1 小时是合法间隔，不该被当成「没设置」
		{2, 6, 54 * time.Hour},
		{0, 0, 7 * 24 * time.Hour},  // 0+0 = 每周
		{-3, 0, 7 * 24 * time.Hour}, // 负数同理
	}
	for _, c := range cases {
		a := newTestApp(t)
		a.Store.Data.UpdateIntervalDays = c.days
		a.Store.Data.UpdateIntervalHours = c.hours
		if got := a.updateInterval(); got != c.want {
			t.Fatalf("%d 天 %d 小时 => %v，期望 %v", c.days, c.hours, got, c.want)
		}
	}

	// 清理间隔下限 1 小时
	a := newTestApp(t)
	a.Store.Data.AutoPruneIntervalHours = 0
	if got := a.pruneInterval(); got != 24*time.Hour {
		t.Fatalf("清理间隔 0 小时 => %v，期望 24h", got)
	}
	a.Store.Data.AutoPruneIntervalHours = 6
	if got := a.pruneInterval(); got != 6*time.Hour {
		t.Fatalf("清理间隔 6 小时 => %v，期望 6h", got)
	}
}

// TestSetAutoUpdateStateMachine 收藏状态机：
// 第一次收藏 → 排期；取消最后一个收藏 → 清空排期；排期已存在时再收藏不覆盖。
func TestSetAutoUpdateStateMachine(t *testing.T) {
	a := newTestApp(t)
	if got := a.SetAutoUpdate("nginx", true); got == "" {
		t.Fatal("首次收藏应当写入 NextRun")
	}
	first := a.Store.Data.AutoUpdateNextRun

	// 再收藏一个：不该把已有排期往后推
	if got := a.SetAutoUpdate("redis", true); got != first {
		t.Fatalf("二次收藏改动了排期: %s != %s", got, first)
	}

	// 取消一个，还剩一个：排期保留
	if got := a.SetAutoUpdate("nginx", false); got != first {
		t.Fatalf("还剩收藏时排期不应清空，得到 %q", got)
	}

	// 取消最后一个：排期清空
	if got := a.SetAutoUpdate("redis", false); got != "" {
		t.Fatalf("收藏清空后 NextRun 应为空，得到 %q", got)
	}

	// 重新收藏：重新排期
	again := a.SetAutoUpdate("nginx", true)
	if again == "" {
		t.Fatal("重新收藏应重新排期")
	}
}

func TestAutoTargets(t *testing.T) {
	a := newTestApp(t)
	a.Store.Data.AutoUpdate = map[string]bool{"c": true, "a": true, "b": false, "": false}
	got := a.autoTargets()
	want := []string{"a", "c"}
	if len(got) != len(want) {
		t.Fatalf("autoTargets = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("autoTargets = %v（未排序或含未启用项），期望 %v", got, want)
		}
	}
}

func TestResetUpdateSchedule(t *testing.T) {
	a := newTestApp(t)
	a.Store.Data.UpdateIntervalDays = 1
	a.Store.Data.UpdateIntervalHours = 0
	a.Store.Data.AutoUpdateLastRun = time.Now().Add(-2 * time.Hour).Format(timeLayout)
	a.Store.Data.AutoUpdateNextRun = time.Now().Add(20 * time.Hour).Format(timeLayout)

	a.Store.Data.UpdateIntervalDays = 0
	a.Store.Data.UpdateIntervalHours = 6
	a.resetUpdateSchedule()

	last, _ := parseStoreTime(a.Store.Data.AutoUpdateLastRun)
	next, ok := parseStoreTime(a.Store.Data.AutoUpdateNextRun)
	if !ok {
		t.Fatalf("resetUpdateSchedule 未写入可解析的 NextRun: %q", a.Store.Data.AutoUpdateNextRun)
	}
	if d := next.Sub(last); d != 6*time.Hour {
		t.Fatalf("重排后间隔 = %v，期望 6h", d)
	}

	// 没有 LastRun 时以「现在」为基准
	b := newTestApp(t)
	b.Store.Data.UpdateIntervalDays = 0
	b.Store.Data.UpdateIntervalHours = 3
	b.resetUpdateSchedule()
	nb, ok := parseStoreTime(b.Store.Data.AutoUpdateNextRun)
	if !ok {
		t.Fatal("无 LastRun 时也应写入 NextRun")
	}
	if d := time.Until(nb); d < 2*time.Hour+50*time.Minute || d > 3*time.Hour+time.Minute {
		t.Fatalf("无 LastRun 时应以现在为基准排期，实际距今 %v", d)
	}
}

func TestBakTag(t *testing.T) {
	cases := map[string]string{
		"latest": "latest-bak",
		"alpine": "alpine-bak",
		"1.2.3":  "1.2.3-bak",
		"v1.0.2": "v1.0.2-bak",
	}
	for in, want := range cases {
		if got := bakTagFor(in); got != want {
			t.Fatalf("bakTagFor(%q) = %q，期望 %q", in, got, want)
		}
	}
	if !isBakTag("nginx:alpine-bak") || !isBakTag("ghcr.io/a/b:latest-bak") {
		t.Fatal("isBakTag 漏判回滚标签")
	}
	for _, notBak := range []string{"nginx:alpine", "nginx:latest", "nginx:bak", "nginx:-bak"} {
		if notBak == "nginx:-bak" {
			continue // 罕见但确实以 -bak 结尾
		}
		if isBakTag(notBak) {
			t.Fatalf("isBakTag(%q) 误判为回滚标签", notBak)
		}
	}
}

func TestPruneCandidates(t *testing.T) {
	imgs := []DockerImage{
		{ID: "sha256:used", RepoTags: []string{"nginx:alpine"}},    // 有容器在用
		{ID: "sha256:dangling", RepoTags: nil},                     // 悬空
		{ID: "sha256:none", RepoTags: []string{"<none>:<none>"}},   // 悬空（显式 none）
		{ID: "sha256:bak", RepoTags: []string{"nginx:alpine-bak"}}, // 回滚镜像
		{ID: "sha256:idle", RepoTags: []string{"redis:alpine"}},    // 有标签但没人用
	}
	inUse := map[string]bool{"sha256:used": true}

	// 模式一：只清悬空
	ids, _ := pruneCandidates(imgs, inUse, false, true)
	if len(ids) != 2 || ids[0] != "sha256:dangling" || ids[1] != "sha256:none" {
		t.Fatalf("仅悬空模式挑选错误: %v", ids)
	}

	// 模式二：清所有未使用的，但保留回滚镜像
	ids, skipped := pruneCandidates(imgs, inUse, true, true)
	if len(ids) != 3 {
		t.Fatalf("全清模式应挑 3 个（dangling/none/idle），实际 %v", ids)
	}
	for _, id := range ids {
		if id == "sha256:used" || id == "sha256:bak" {
			t.Fatalf("不该删 %s", id)
		}
	}
	if len(skipped) != 1 {
		t.Fatalf("应记录 1 个被保留的回滚镜像，实际 %v", skipped)
	}

	// 模式三：连回滚镜像一起清
	ids, skipped = pruneCandidates(imgs, inUse, true, false)
	if len(ids) != 4 || len(skipped) != 0 {
		t.Fatalf("不保留回滚镜像时应挑 4 个且无跳过，实际 %v / %v", ids, skipped)
	}
}

func TestStreamError(t *testing.T) {
	// /images/create 失败时是一串 JSON 行，真正的错误在最后一行
	raw := []byte(`{"status":"Pulling from library/alpine","id":"latest"}
{"status":"Already exists","progress":"[==>  ]","id":"e2de96513ba9"}
{"errorDetail":{"message":"toomanyrequests: You have reached your unauthenticated pull rate limit."},"error":"toomanyrequests: You have reached your unauthenticated pull rate limit."}`)
	got := streamError(raw)
	want := "toomanyrequests: You have reached your unauthenticated pull rate limit."
	if got != want {
		t.Fatalf("streamError = %q，期望 %q", got, want)
	}

	// 只有 error 字段没有 errorDetail
	if got := streamError([]byte(`{"error":"manifest unknown"}`)); got != "manifest unknown" {
		t.Fatalf("streamError = %q，期望 manifest unknown", got)
	}

	// 完全不是 JSON（例如 nginx 返回的 HTML）
	if got := streamError([]byte("<html>502</html>")); got != "<html>502</html>" {
		t.Fatalf("streamError = %q", got)
	}

	// 空 body
	if got := streamError(nil); got != "daemon 未返回任何内容" {
		t.Fatalf("streamError(nil) = %q", got)
	}
}

// TestCreateContainerBody 锁住 POST /containers/create 的请求体形状。
//
// v1.0.0～v1.0.2 一直发的是 {"Config":{...},"HostConfig":{...}}，daemon 回
// `config cannot be empty in order to create a container`，
// 导致「一键更新」从未真正重建出容器。这个用例就是为了防止再改回去。
func TestCreateContainerBody(t *testing.T) {
	config := map[string]any{
		"Image":    "alpine:latest", // inspect 结果里本来就带，应以入参覆盖为准
		"Cmd":      []any{"sleep", "3600"},
		"Env":      []any{"DC_DEMO=true"},
		"Labels":   map[string]any{"dc.demo": "true"},
		"Hostname": "abc123", // 只读字段，必须被清掉
		"Tty":      false,
		"Healthcheck": map[string]any{
			"Test": []any{"NONE"},
		},
		"NetworkDisabled": false,
	}
	hostCfg := map[string]any{
		"Memory":          float64(33554432),
		"RestartPolicy":   map[string]any{"Name": "no"},
		"ContainerIDFile": "/x", // 只读字段，必须被清掉
		"AutoRemove":      true,
	}

	body := createContainerBody(config, hostCfg, "alpine:latest")

	// 1) 顶层必须有 Image，且是入参给的那个
	if body["Image"] != "alpine:latest" {
		t.Fatalf("顶层 Image = %v，期望 alpine:latest", body["Image"])
	}

	// 2) ★ 顶层绝对不能出现 Config 键（老 bug 的形状）
	if _, bad := body["Config"]; bad {
		t.Fatalf("顶层出现了 Config 键，这正是 daemon 拒绝的形状：%#v", body)
	}

	// 3) Config 字段应平铺在顶层
	if _, ok := body["Cmd"]; !ok {
		t.Fatalf("Cmd 未被平铺到顶层：%#v", body)
	}
	if _, ok := body["Env"]; !ok {
		t.Fatalf("Env 未被平铺到顶层：%#v", body)
	}
	if _, ok := body["Labels"]; !ok {
		t.Fatalf("Labels 未被平铺到顶层：%#v", body)
	}

	// 4) HostConfig 必须是嵌套的，且只读字段被清掉
	hc, ok := body["HostConfig"].(map[string]any)
	if !ok {
		t.Fatalf("HostConfig 未嵌套或被换成了别的类型：%#v", body["HostConfig"])
	}
	if hc["Memory"] != float64(33554432) {
		t.Fatalf("HostConfig.Memory 丢失：%#v", hc)
	}
	if _, bad := hc["ContainerIDFile"]; bad {
		t.Fatalf("HostConfig 里残留只读字段 ContainerIDFile")
	}
	if _, bad := hc["AutoRemove"]; bad {
		t.Fatalf("HostConfig 里残留只读字段 AutoRemove")
	}

	// 5) 顶层只读字段必须被剔除
	for _, k := range []string{"Hostname", "Tty", "Healthcheck", "NetworkDisabled"} {
		if _, bad := body[k]; bad {
			t.Fatalf("顶层残留只读字段 %s：%#v", k, body)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1024:       "1.0 KB",
		1536:       "1.5 KB",
		1048576:    "1.0 MB",
		1073741824: "1.0 GB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Fatalf("humanBytes(%d) = %q，期望 %q", in, got, want)
		}
	}
}
