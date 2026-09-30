package main

// 更新检查的回归测试。
//
// 背景：旧实现把取 token 的地址硬编码成 auth.docker.io，且只在
// host == registry-1.docker.io 时才附带 Authorization，导致 GHCR / Quay 等
// 非 Docker Hub 仓库一律返回 401，更新状态永远显示「未知」。
// 修复后改为按 registry 返回的 WWW-Authenticate 挑战动态取 token，
// 本文件锁定该解析逻辑，防止回归。

import "testing"

func TestParseAuthChallenge(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		realm   string
		service string
		scope   string
	}{
		{
			name:    "ghcr",
			header:  `Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:fujiwareshine/docker-control:pull"`,
			realm:   "https://ghcr.io/token",
			service: "ghcr.io",
			scope:   "repository:fujiwareshine/docker-control:pull",
		},
		{
			name:    "docker hub",
			header:  `Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/nginx:pull"`,
			realm:   "https://auth.docker.io/token",
			service: "registry.docker.io",
			scope:   "repository:library/nginx:pull",
		},
		{
			name:    "quay 无 scope",
			header:  `Bearer realm="https://quay.io/v2/auth",service="quay.io"`,
			realm:   "https://quay.io/v2/auth",
			service: "quay.io",
			scope:   "",
		},
		{
			name:    "realm 带查询串（含逗号，需按引号状态切分）",
			header:  `Bearer realm="https://example.com/token?a=1,b=2",service="example.com",scope="repository:x/y:pull"`,
			realm:   "https://example.com/token?a=1,b=2",
			service: "example.com",
			scope:   "repository:x/y:pull",
		},
		{
			name:    "值不加引号也应能解析",
			header:  `Bearer realm=https://r.example/token,service=svc,scope=repository:a/b:pull`,
			realm:   "https://r.example/token",
			service: "svc",
			scope:   "repository:a/b:pull",
		},
		{
			name:   "大小写不敏感",
			header: `bearer realm="https://r.example/token"`,
			realm:  "https://r.example/token",
		},
		{name: "非 Bearer 挑战（Basic）", header: `Basic realm="x"`},
		{name: "空头", header: ""},
		{name: "只有 scheme 没有参数", header: "Bearer"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			realm, service, scope := parseAuthChallenge(c.header)
			if realm != c.realm || service != c.service || scope != c.scope {
				t.Fatalf("parseAuthChallenge(%q) = (%q, %q, %q)，期望 (%q, %q, %q)",
					c.header, realm, service, scope, c.realm, c.service, c.scope)
			}
		})
	}
}
