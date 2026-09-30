package main

// 宿主机命令执行器。
//
// 背景：面板运行在容器内（host 网络 + 挂载 docker.sock），但**没有**宿主机
// PID 命名空间，因此容器内直接调用 nsenter 会因缺少 CAP_SYS_ADMIN 失败：
//     nsenter: setns(): can't reassociate to namespace 'ipc': Operation not permitted
//
// 目标：在不给面板容器 privileged 的前提下，仍能操作宿主机（例如重启 dockerd）。
//
// 方案：通过 docker.sock 起一个**一次性特权容器**（privileged + pid=host），
// 让它进入宿主机命名空间执行命令，执行完自动删除。这是 Portainer 等面板的
// 标准做法——权限只授予临时容器，而不是常驻的面板本身。
//
// 镜像选择：优先复用面板自身镜像（本地必然存在，无需联网拉取）。
// 若镜像内没有 nsenter（非 alpine 基础镜像），回退到常见工具镜像。

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

// base64Encode / base64Decode 便于在宿主机命令里安全传递文件内容
func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.TrimSpace(s))
}

// hostExecImage 执行宿主机命令所用的镜像。
// 优先用面板自身镜像：本地已存在、必然有 shell，避免运行时联网拉取。
func hostExecImage() string {
	return resolveExecImage()
}

// readHostFile 通过特权容器读取宿主机上的文件内容。
//
// 用途：窄挂载部署时，容器内看不到宿主机 /etc，无法直接 os.ReadFile。
// 这里借用一次性特权容器进入宿主机命名空间后 cat 文件。
// 文件不存在时返回 ("", false, nil)，不算错误。
func (a *App) readHostFile(path string) (string, bool, error) {
	// base64 编码避免二进制/换行干扰
	cmd := fmt.Sprintf("if [ -f %s ]; then base64 -w0 %s; else echo __NOFILE__; fi",
		shellQuote(path), shellQuote(path))
	out, err := a.execOnHost(cmd)
	if err != nil {
		return "", false, err
	}
	out = strings.TrimSpace(out)
	if out == "__NOFILE__" {
		return "", false, nil
	}
	dec, derr := base64Decode(out)
	if derr != nil {
		return "", false, derr
	}
	return string(dec), true, nil
}

// writeHostFile 通过特权容器把内容写入宿主机文件（自动创建父目录）。
func (a *App) writeHostFile(path, content string) error {
	b64 := base64Encode([]byte(content))
	cmd := fmt.Sprintf("mkdir -p %s && printf %%s %s | base64 -d > %s",
		shellQuote(filepathDir(path)), shellQuote(b64), shellQuote(path))
	_, err := a.execOnHost(cmd)
	return err
}

// removeHostFile 通过特权容器删除宿主机文件。
func (a *App) removeHostFile(path string) error {
	cmd := fmt.Sprintf("rm -f %s && rmdir %s 2>/dev/null || true",
		shellQuote(path), shellQuote(filepathDir(path)))
	_, err := a.execOnHost(cmd)
	return err
}

// shellQuote 单引号包裹，转义内部单引号，防止命令注入。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// filepathDir 取路径的父目录（不经 filepath 包，保持 POSIX 语义）
func filepathDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

// execOnHost 在宿主机命名空间执行 shell 命令，返回合并输出。
//
// 实现要点：
//  1. 创建 privileged + pid=host + network=host 的短命容器
//  2. 用 nsenter 进入宿主机 PID 1 的各命名空间后执行 command
//  3. 等待容器退出，读取日志作为输出
//  4. 无论成败都删除容器
//
// 注意：命令自身若会杀死调用方（如 restart docker），输出可能拿不到全，
// 调用方需容错。
func (a *App) execOnHost(command string) (string, error) {
	if a.Docker == nil {
		return "", fmt.Errorf("Docker 客户端不可用")
	}
	img := hostExecImage()

	// nsenter 参数：进入宿主机 PID 1 的挂载/UTS/IPC/网络/PID 命名空间
	nsArgs := []string{
		"-t", "1", "-m", "-u", "-i", "-n", "-p", "--",
		"sh", "-c", command,
	}

	cfg := map[string]any{
		"Image":      img,
		"Entrypoint": []string{"nsenter"},
		"Cmd":        nsArgs,
		"HostConfig": map[string]any{
			"Privileged":  true,
			"PidMode":     "host",
			"NetworkMode": "host",
			"AutoRemove":  false, // 手动删除，便于读取退出码与日志
		},
		"Tty": false,
	}

	// 1) 创建容器
	var created struct {
		ID string `json:"Id"`
	}
	err := a.Docker.doJSON("POST", "/containers/create",
		url.Values{"name": {"docker-control-hostexec"}}, cfg, &created)
	if err != nil {
		// 同名容器可能残留，先清理再重试一次
		a.Docker.doJSON("DELETE", "/containers/docker-control-hostexec",
			url.Values{"force": {"1"}}, nil, nil)
		err = a.Docker.doJSON("POST", "/containers/create",
			url.Values{"name": {"docker-control-hostexec"}}, cfg, &created)
		if err != nil {
			return "", fmt.Errorf("创建宿主机执行容器失败: %w", err)
		}
	}
	id := created.ID
	defer func() {
		a.Docker.doJSON("DELETE", "/containers/"+id,
			url.Values{"force": {"1"}, "v": {"1"}}, nil, nil)
	}()

	// 2) 启动
	if err := a.Docker.doJSON("POST", "/containers/"+id+"/start", nil, nil, nil); err != nil {
		return "", fmt.Errorf("启动执行容器失败: %w", err)
	}

	// 3) 等待退出（最长 30s）
	deadline := time.Now().Add(30 * time.Second)
	var code int
	for {
		var st struct {
			Running  bool `json:"Running"`
			ExitCode int  `json:"ExitCode"`
		}
		if err := a.Docker.doJSON("GET", "/containers/"+id+"/json", nil, nil, &st); err == nil {
			if !st.Running {
				code = st.ExitCode
				break
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("宿主机命令执行超时")
		}
		time.Sleep(300 * time.Millisecond)
	}

	// 4) 读日志
	resp, err := a.Docker.do("GET", "/containers/"+id+"/logs",
		url.Values{"stdout": {"1"}, "stderr": {"1"}}, nil)
	out := ""
	if err == nil && resp != nil {
		var buf bytes.Buffer
		demuxDockerStream(resp.Body, &buf)
		resp.Body.Close()
		out = strings.TrimSpace(buf.String())
	}

	if code != 0 {
		msg := out
		if msg == "" {
			msg = fmt.Sprintf("退出码 %d", code)
		}
		return out, fmt.Errorf("宿主机命令执行失败: %s", msg)
	}
	return out, nil
}

// demuxDockerStream 解析 Docker logs 的多路复用流。
// Docker 默认返回 8 字节头（stream type + 3 padding + 4 byte length），
// 但也可能返回裸文本（TTY 模式），这里做兼容处理。
func demuxDockerStream(r io.Reader, w io.Writer) {
	raw, err := io.ReadAll(r)
	if err != nil || len(raw) == 0 {
		return
	}
	// 检测是否为多路复用格式：首个字节应为 0/1/2，且第 2-4 字节为 0
	if len(raw) >= 8 && (raw[0] == 0 || raw[0] == 1 || raw[0] == 2) &&
		raw[1] == 0 && raw[2] == 0 && raw[3] == 0 {
		i := 0
		for i+8 <= len(raw) {
			size := int(raw[i+4])<<24 | int(raw[i+5])<<16 | int(raw[i+6])<<8 | int(raw[i+7])
			i += 8
			if i+size > len(raw) {
				w.Write(raw[i:])
				return
			}
			w.Write(raw[i : i+size])
			i += size
		}
		return
	}
	w.Write(raw)
}

// hasHostExec 判断当前是否具备「无 privileged 操作宿主机」的能力。
// 条件：docker.sock 可用 + 目标镜像存在。
func (a *App) hasHostExec() bool {
	if a.Docker == nil {
		return false
	}
	img := hostExecImage()
	resp, err := a.Docker.do("GET", "/images/"+url.PathEscape(img)+"/json", nil, nil)
	if err != nil {
		return false
	}
	if resp != nil {
		defer resp.Body.Close()
		return resp.StatusCode == 200
	}
	return false
}

// selfImageName 读取面板容器自身的镜像名，供 hostexec 复用。
// 读取失败时返回空串，由调用方回退到默认值。
func (a *App) selfImageName() string {
	if a.Docker == nil {
		return ""
	}
	resp, err := a.Docker.do("GET", "/containers/"+a.SelfName+"/json", nil, nil)
	if err != nil || resp == nil {
		return ""
	}
	defer resp.Body.Close()
	var info struct {
		Image  string `json:"Image"`
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
	}
	if json.NewDecoder(resp.Body).Decode(&info) != nil {
		return ""
	}
	if info.Config.Image != "" {
		return info.Config.Image
	}
	return info.Image
}

// selfExecImage 在进程启动时确定并缓存 hostexec 使用的镜像。
var cachedExecImage string

func resolveExecImage() string {
	if cachedExecImage != "" {
		return cachedExecImage
	}
	if v := os.Getenv("HOST_EXEC_IMAGE"); v != "" {
		cachedExecImage = v
		return cachedExecImage
	}
	if app != nil {
		if n := app.selfImageName(); n != "" {
			cachedExecImage = n
			return cachedExecImage
		}
	}
	cachedExecImage = envOr("SELF_IMAGE", "docker-control:local")
	return cachedExecImage
}
