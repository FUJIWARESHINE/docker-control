# Docker Control v1.0 — 轻量自研 Docker 管理面板

完全自研的 Docker 管理面板：**Go 后端直连 Docker Engine API + 全新流光玻璃风格前端**。
零面板依赖（仅标准库 + gorilla/websocket），单二进制 + 静态文件，镜像约 30MB。

> v1.0 为完全重构版本：前后端全部重写，UI 采用自研「流光玻璃拟态」设计语言
> （深色/浅色双主题、品牌渐变、按钮流光、径向氛围光）。

## 功能一览

- **仪表板**：统计卡 / 容器 CPU·内存·网络速率实时横条（4s 轮询）/ 实时操作日志（WebSocket）
- **容器**：列表 / 启停 / 重启 / 强杀 / 暂停恢复 / 删除 / 批量操作 / inspect / 日志流（可跟随）/
  单容器统计 / 一键生成 compose / 创建容器（模板）
- **镜像**：列表 / 拉取（WS 进度）/ tag / 删除 / 清理未使用 / tar 导入
- **网络与卷**：可视化管理、清理未使用资源
- **端口页**：`/proc/net/tcp|udp` 扫描 + Engine API 端口映射对照，自定义备注
- **Compose 项目**：label 聚合展示 / 启停 / down / 文件编辑 / 聚合日志
- **更新中心**：registry digest 对比检查更新、重建式容器更新（拉取→停→删→建→启）、自动更新调度
- **系统**：密码登录（Bearer + Cookie 会话）、API Key、容器别名、部署模板、定时任务、
  操作日志、配置备份下载、面板自保护（自身容器 API 层剔除，无法误停/误删）

## 目录结构

```
panel/
├── backend/              # ★ 自研 Go 后端
│   ├── main.go           # 入口 / 静态服务
│   ├── routes.go         # 路由注册与子路由分发
│   ├── auth.go           # 登录 / 会话 / API Key 鉴权
│   ├── docker.go         # Docker Engine API 客户端（unix socket / tcp）
│   ├── containers.go     # 容器列表/操作/inspect/统计/日志/生成compose
│   ├── images.go         # 镜像管理（拉取进度走 WS）
│   ├── networks_volumes.go / ports.go
│   ├── compose.go        # Compose 项目（label 聚合）
│   ├── updates.go        # 更新检查 / 重建式更新 / 自动更新调度
│   ├── prefs.go          # 配置/别名/模板/定时任务/APIKey/日志/备份
│   ├── store.go          # JSON 持久化（原子写）
│   └── ws.go             # WebSocket 广播中心（连接需 token）
├── static/               # ★ 全新前端（流光玻璃风格）
│   ├── login.html        # 登录页
│   ├── index.html        # SPA 主壳
│   ├── css/style.css     # 设计系统（深/浅双主题）
│   └── js/               # api / ui / ws / app + views/*
├── Dockerfile            # 多阶段构建：golang:1.23-alpine → alpine
├── docker-compose.yml    # 部署文件（host 网络安全版）
└── .github/workflows/    # push main 自动构建 GHCR 镜像
```

## 部署（NAS / Linux 服务器）

1. 把 `docker-compose.yml` 传到服务器，`docker compose up -d`
2. 访问 `http://<IP>:9527`，**默认密码 `admin`，登录后请立即在系统设置中修改**
3. 更新版本：`docker compose pull && docker compose up -d`

## 安全设计

- 无 privileged：`cap_drop: ALL` + `no-new-privileges` + 非 root 用户运行
- 最小挂载：只挂 docker.sock + 数据目录，不挂宿主机根目录
- WebSocket 连接需携带会话 token（v1.0 补强）
- 面板自身容器在 API 层从列表剔除（`docker-control.self` 标记）

## 已知限制（诚实清单）

- 检查更新只对 docker.io 及匿名可读仓库有效，配镜像加速/私有仓库会显示「未知」
- host 网络容器的每容器网络计数 Engine API 不提供，网速显示为 0
- 定时任务调度为 interval 轮询的简化实现，非完整 cron 解析
- 本机调试：`go build` 后 `DATA_DIR=./data STATIC_DIR=./static PORT=9527 ./docker-control`
