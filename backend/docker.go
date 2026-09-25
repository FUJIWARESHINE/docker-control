package main

// Docker Engine API 客户端（直连 unix socket / tcp，零第三方依赖）
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DockerClient struct {
	base string // http://docker.sock 的占位 host
	scheme string // unix | tcp
	addr   string
	client *http.Client
}

func NewDockerClient(host string) *DockerClient {
	dc := &DockerClient{}
	if strings.HasPrefix(host, "unix://") {
		dc.scheme = "unix"
		dc.addr = strings.TrimPrefix(host, "unix://")
		dc.base = "http://docker"
		tr := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", dc.addr)
			},
		}
		dc.client = &http.Client{Transport: tr, Timeout: 0} // 流式接口不设整体超时
	} else {
		dc.scheme = "tcp"
		dc.addr = strings.TrimPrefix(host, "tcp://")
		dc.base = "http://" + dc.addr
		dc.client = &http.Client{Timeout: 0}
	}
	return dc
}

// do 执行 Engine API 请求，返回完整响应（调用方负责关闭 Body）
func (dc *DockerClient) do(method, path string, query url.Values, body any) (*http.Response, error) {
	u := dc.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return dc.client.Do(req)
}

// doJSON 请求并解析 JSON 到 out
func (dc *DockerClient) doJSON(method, path string, query url.Values, body, out any) error {
	resp, err := dc.do(method, path, query, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("docker api %s %d: %s", path, resp.StatusCode, truncate(string(data), 300))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------- 核心数据结构（Engine API 子集） ----------

type DockerContainer struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Command string            `json:"Command"`
	Created int64             `json:"Created"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Ports   []DockerPort      `json:"Ports"`
	Labels  map[string]string `json:"Labels"`
	Mounts  []DockerMount     `json:"Mounts"`
}

type DockerPort struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

type DockerMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Mode        string `json:"Mode"`
	RW          bool   `json:"RW"`
}

type DockerImage struct {
	ID        string      `json:"Id"`
	RepoTags  []string    `json:"RepoTags"`
	Created   int64       `json:"Created"`
	Size      int64       `json:"Size"`
	Labels    map[string]string `json:"Labels"`
	RepoDigests []string  `json:"RepoDigests"`
}

type DockerNetwork struct {
	Name       string                        `json:"Name"`
	ID         string                        `json:"Id"`
	Created    string                        `json:"Created"`
	Driver     string                        `json:"Driver"`
	Internal   bool                          `json:"Internal"`
	Containers map[string]DockerNetContainer `json:"Containers"`
}

type DockerNetContainer struct {
	Name        string `json:"Name"`
	IPv4Address string `json:"IPv4Address"`
}

// ListContainers 全量容器列表
func (dc *DockerClient) ListContainers(all bool) ([]DockerContainer, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	var out []DockerContainer
	err := dc.doJSON("GET", "/containers/json", q, nil, &out)
	return out, err
}

// ListImages
func (dc *DockerClient) ListImages(all bool) ([]DockerImage, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	var out []DockerImage
	err := dc.doJSON("GET", "/images/json", q, nil, &out)
	return out, err
}

// ListNetworks
func (dc *DockerClient) ListNetworks() ([]DockerNetwork, error) {
	var out []DockerNetwork
	err := dc.doJSON("GET", "/networks", nil, nil, &out)
	return out, err
}

// ContainerAction start/stop/restart/kill/pause/unpause
func (dc *DockerClient) ContainerAction(id, action string, timeoutSec int) error {
	q := url.Values{}
	if action == "stop" && timeoutSec > 0 {
		q.Set("t", fmt.Sprint(timeoutSec))
	}
	resp, err := dc.do("POST", "/containers/"+id+"/"+action, q, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 304 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %s", action, id[:12], truncate(string(b), 200))
	}
	return nil
}

// RemoveContainer
func (dc *DockerClient) RemoveContainer(id string, force, removeVolumes bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	if removeVolumes {
		q.Set("v", "1")
	}
	resp, err := dc.do("DELETE", "/containers/"+id, q, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("rm %s: %s", id[:12], truncate(string(b), 200))
	}
	return nil
}
