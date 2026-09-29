package main

// 镜像拉取代理设置。
//
// 原理：Docker 的镜像拉取由宿主机 dockerd 完成，面板只是调 Engine API。
// 因此「让拉取走代理」= 给 dockerd 配代理。dockerd 会读取环境变量
// HTTP_PROXY / HTTPS_PROXY / NO_PROXY，systemd 下通过 drop-in 注入。
//
// 落盘位置（宿主机视角）：/etc/systemd/system/docker.service.d/http-proxy.conf
// 面板容器内通过 HOST_ROOT 前缀访问宿主机文件系统。
//
// 生效方式：写文件 → systemctl daemon-reload → systemctl restart docker。
// 注意：重启 dockerd 会重启所有容器（restart 策略为 always 的会自动回来）。

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// proxyConfRelPath 代理配置在宿主机上的相对路径
const proxyConfRelPath = "/etc/systemd/system/docker.service.d/http-proxy.conf"

type ProxyConfig struct {
	Enabled  bool   `json:"enabled"`
	HTTP     string `json:"http_proxy"`
	HTTPS    string `json:"https_proxy"`
	NoProxy  string `json:"no_proxy"`
	Applied  bool   `json:"applied"`  // 配置文件是否存在
	HostPath string `json:"host_path"` // 宿主机上的真实路径
}

// hostFilePath 把「宿主机绝对路径」映射为「容器内可访问的路径」。
//
// 仅当部署时设置了 HOST_ROOT（宽挂载 /:/host:rw 方案）才返回可用路径。
// 窄挂载部署下宿主机 /etc 不可见，这里返回 false，由调用方回退到宿主机执行器。
func hostFilePath(hostPath string) (string, bool) {
	root := envOr("HOST_ROOT", "")
	if root == "" {
		return "", false
	}
	if _, err := os.Stat(root); err != nil {
		return "", false
	}
	return filepath.Join(root, hostPath), true
}

// readProxyConfig 读取当前代理配置。
//
// 两条路径：
//  1. 容器内能直接访问宿主机文件（宽挂载 /:/host:rw）→ 直接读，快；
//  2. 窄挂载部署（容器看不到宿主机 /etc）→ 借特权容器读。
func (a *App) readProxyConfig() ProxyConfig {
	cfg := ProxyConfig{HostPath: proxyConfRelPath}

	var content string
	var got bool
	if real, ok := hostFilePath(proxyConfRelPath); ok {
		if b, err := os.ReadFile(real); err == nil {
			content, got = string(b), true
		}
	}
	if !got {
		// 回退：通过宿主机执行器读取
		if s, exists, err := a.readHostFile(proxyConfRelPath); err == nil && exists {
			content, got = s, true
		}
	}
	if !got {
		return cfg
	}

	cfg.Applied = true
	cfg.Enabled = true
	// 解析 Environment= 行里的 KEY=VALUE
	re := regexp.MustCompile(`(?m)^Environment="?([A-Za-z_]+)=([^"\n]*)"?`)
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		k, v := m[1], strings.TrimSpace(m[2])
		switch strings.ToUpper(k) {
		case "HTTP_PROXY":
			cfg.HTTP = v
		case "HTTPS_PROXY":
			cfg.HTTPS = v
		case "NO_PROXY":
			cfg.NoProxy = v
		}
	}
	// 覆盖：如果用户在 Store 里显式关掉了，即使文件存在也视为未启用
	a.Store.mu.RLock()
	if v, ok := a.Store.Data.UserPrefs["proxy_enabled"].(bool); ok {
		cfg.Enabled = v
	}
	a.Store.mu.RUnlock()
	return cfg
}

// defaultNoProxy 总是附加到 NO_PROXY 的条目。
// 镜像加速器与内网地址必须绕过代理，否则一开代理连镜像都拉不动
// （实测：dockerd 会拿 http_proxy 去连加速器域名，导致 connection refused）。
var defaultNoProxy = []string{
	"localhost", "127.0.0.1", "::1",
	"*.local", "*.internal", "*.lan",
	// 常见国内镜像加速器
	"docker.m.daocloud.io", "docker.1ms.run", "dockerproxy.com",
	"docker.io", "registry-1.docker.io", "gcr.io", "ghcr.io", "quay.io",
}

// mergeNoProxy 把用户填写的 NO_PROXY 与默认白名单合并去重。
func mergeNoProxy(user string) string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			return
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	for _, s := range strings.Split(user, ",") {
		add(s)
	}
	for _, s := range defaultNoProxy {
		add(s)
	}
	return strings.Join(out, ",")
}

// buildProxyContent 生成 dockerd drop-in 的配置内容
func buildProxyContent(httpP, httpsP, noP string) string {
	noP = mergeNoProxy(noP)
	var sb strings.Builder
	sb.WriteString("# 由 docker-control 面板自动生成，请勿手工修改\n")
	sb.WriteString("[Service]\n")
	if httpP != "" {
		sb.WriteString(fmt.Sprintf("Environment=\"HTTP_PROXY=%s\"\n", httpP))
	}
	if httpsP != "" {
		sb.WriteString(fmt.Sprintf("Environment=\"HTTPS_PROXY=%s\"\n", httpsP))
	}
	if noP != "" {
		sb.WriteString(fmt.Sprintf("Environment=\"NO_PROXY=%s\"\n", noP))
	}
	return sb.String()
}

// writeProxyConfig 写宿主机 drop-in 文件（不重启，仅落盘）。
//
// 优先直接写文件（宽挂载）；写不到时借特权容器写（窄挂载）。
func (a *App) writeProxyConfig(httpP, httpsP, noP string) error {
	content := buildProxyContent(httpP, httpsP, noP)

	// 路径 1：容器内可直接写宿主机文件（宽挂载 /:/host:rw）
	if real, ok := hostFilePath(proxyConfRelPath); ok {
		if err := os.MkdirAll(filepath.Dir(real), 0755); err == nil {
			if err := os.WriteFile(real, []byte(content), 0644); err == nil {
				return nil
			}
		}
	}

	// 路径 2：借宿主机执行器写入（窄挂载部署）
	if err := a.writeHostFile(proxyConfRelPath, content); err != nil {
		return fmt.Errorf("写入代理配置失败：%w（需要宿主机执行镜像可用，或改用 /:/host:rw 挂载）", err)
	}
	return nil
}

// clearProxyConfig 删除宿主机 drop-in 文件
func (a *App) clearProxyConfig() error {
	// 路径 1：直接删
	if real, ok := hostFilePath(proxyConfRelPath); ok {
		if err := os.Remove(real); err == nil || os.IsNotExist(err) {
			return nil
		}
	}
	// 路径 2：借宿主机执行器删（窄挂载）
	if err := a.removeHostFile(proxyConfRelPath); err != nil {
		return err
	}
	return nil
}

// hasHostPidNS 判断面板容器是否共享了宿主机 PID 命名空间。
//
// 只有当容器以 --pid=host（或 privileged 附带）启动时，/proc/1 才是宿主机的
// init；否则 /proc/1 是容器自己的入口进程。据此判断能否直接 nsenter。
func (a *App) hasHostPidNS() bool {
	b, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		return false
	}
	comm := strings.TrimSpace(string(b))
	// 容器自己的入口是 docker-control；宿主机 init 通常是 systemd/init
	return comm != "" && comm != "docker-control"
}

// reloadDockerProxy 让代理配置在宿主机上真正生效。
//
// 分两级尝试：
//  1. 直接 nsenter 进宿主机 PID 1 —— 仅当面板容器带 hostPID/privileged 时可用；
//  2. 通过 docker.sock 起一次性特权容器代执行 —— 面板无需 privileged 也能工作（默认路径）。
//
// 关键设计：重启 dockerd 会连带杀掉发起它的容器自身。如果用同步的
// `systemctl restart docker`，命令会在重启途中被 SIGTERM，dockerd 会卡在
// deactivating 状态。因此这里用 `systemctl --no-block`（异步投递，立即返回），
// 让重启动作完全由宿主机 systemd 接管，与调用方进程树彻底解耦。
//
// applied 语义：命令成功投递即视为生效；面板随后自身会重启，属预期行为。
func (a *App) reloadDockerProxy() (string, error) {
	// --no-block：异步投递，立即返回，不阻塞等待服务状态变化
	script := "systemctl --no-block daemon-reload && systemctl --no-block restart docker"

	// 路径 1：面板自身共享宿主机 PID 命名空间（privileged / hostPID 部署）
	if a.hasHostPidNS() {
		out, err := exec.Command("nsenter", "--target", "1",
			"--mount", "--uts", "--ipc", "--net", "--pid", "--",
			"sh", "-c", script).CombinedOutput()
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}

	// 路径 2：起一次性特权容器代执行（无需面板 privileged）
	out, err := a.execOnHost(script)
	if err != nil {
		return out, fmt.Errorf("无法自动重启 dockerd：%w。"+
			"配置已写入宿主机，可手动执行：sudo systemctl daemon-reload && sudo systemctl restart docker", err)
	}
	return out, nil
}

// handleProxy GET/POST /api/proxy —— 代理设置读写
func (a *App) handleProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := a.readProxyConfig()
		// 补充 Store 里用户填的值（优先展示用户填写内容）
		a.Store.mu.RLock()
		if v, ok := a.Store.Data.UserPrefs["proxy_http"].(string); ok && v != "" {
			cfg.HTTP = v
		}
		if v, ok := a.Store.Data.UserPrefs["proxy_https"].(string); ok && v != "" {
			cfg.HTTPS = v
		}
		if v, ok := a.Store.Data.UserPrefs["proxy_noproxy"].(string); ok && v != "" {
			cfg.NoProxy = v
		}
		a.Store.mu.RUnlock()
		ok(w, cfg)
		return
	}

	// POST：保存并可选应用
	m := readBody(r)
	action := bodyStr(m, "action")
	httpP := bodyStr(m, "http_proxy")
	httpsP := bodyStr(m, "https_proxy")
	noP := bodyStr(m, "no_proxy")
	enabled := bodyBool(m, "enabled")

	// 落盘到 Store（用户填写的原始值）
	a.Store.mu.Lock()
	if a.Store.Data.UserPrefs == nil {
		a.Store.Data.UserPrefs = map[string]any{}
	}
	a.Store.Data.UserPrefs["proxy_enabled"] = enabled
	a.Store.Data.UserPrefs["proxy_http"] = httpP
	a.Store.Data.UserPrefs["proxy_https"] = httpsP
	a.Store.Data.UserPrefs["proxy_noproxy"] = noP
	a.Store.mu.Unlock()
	a.Store.Save()

	if action == "clear" || !enabled {
		if err := a.clearProxyConfig(); err != nil {
			fail(w, 500, err.Error())
			return
		}
		a.Logs.Add("INFO", "已清除镜像拉取代理，正在异步重启 dockerd", "realtime")
		// 立刻响应客户端，重启在响应送达后异步执行
		writeJSON(w, 200, map[string]any{
			"success": true, "message": "代理已关闭，Docker 正在重启（约 10 秒内生效）",
			"applied": true, "restarting": true,
		})
		a.scheduleDockerReload()
		return
	}

	if httpP == "" && httpsP == "" {
		fail(w, 400, "请至少填写 HTTP 或 HTTPS 代理地址")
		return
	}
	if err := a.writeProxyConfig(httpP, httpsP, noP); err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.Logs.Add("INFO", fmt.Sprintf("已设置镜像拉取代理 %s / %s，正在异步重启 dockerd", httpP, httpsP), "realtime")
	// 立刻响应客户端，重启在响应送达后异步执行
	writeJSON(w, 200, map[string]any{
		"success": true, "message": "代理已保存，Docker 正在重启（约 10 秒内生效）",
		"applied": true, "restarting": true,
	})
	a.scheduleDockerReload()
}

// scheduleDockerReload 异步重启 dockerd。
//
// 为什么必须异步：重启 dockerd 会杀掉面板容器本身，如果同步执行，HTTP 响应
// 根本来不及返回（客户端收到的是连接中断）。这里先延迟 1.5 秒让响应送达，
// 再执行重启；面板随后由 restart=always 自动拉起。
func (a *App) scheduleDockerReload() {
	go func() {
		time.Sleep(1500 * time.Millisecond)
		if out, err := a.reloadDockerProxy(); err != nil {
			a.Logs.Add("WARNING", "重启 dockerd 失败: "+err.Error(), "realtime")
		} else if out != "" {
			a.Logs.Add("INFO", "dockerd 重启指令已投递: "+out, "realtime")
		}
	}()
}
