# Docker Control v0.2 — diancup 基底 + tradis 风格 UI · 纯 Docker 管理版

## 这是什么
- **后端**：原样使用 `yjnas/diancup:latest`（官方已停更，作为二次开发基底）
- **前端**：diancup 原版 static 的完整副本 + 两层改造：
  - **v0.1 皮肤**：注册 `tradis-blue` 主题（Tailwind Slate/Blue 色板，tradis 设计语言），Inter / JetBrains Mono 字体，圆角与柔和阴影
  - **v0.2 纯 Docker 管理改造**：
    - 侧边栏定稿（v0.3）：概览（仪表板）/ 管理（容器、项目、镜像、网络、端口、存储卷、模板创建、定时任务、检查更新、系统设置）/ 其他（关于）；删除 文件管理、主机监控、SSH终端、模板市场、昱君探针、生成Compose
    - 顶部"每日一言"模块移除（含 hitokoto 启动调用）
    - 左下角快捷链接只留 GitHub（href 留空，待放自己的仓库地址）
    - "关于"视图清空留白，待放功能介绍
    - 仪表板聚焦容器：隐藏依赖 `/host` 挂载的系统信息/资源监控栏，容器统计占满首屏
    - 容器状态监控横条化（tradis 风格）：名称+短ID / 镜像 / 状态点 / CPU·内存 / 端口标签 / tradis 风格操作按钮（红=停止·绿=启动、黄=重启、更多菜单：自动更新·别名·图标·检查更新·更新容器）
    - 可更新行橙色左边框高亮，已停止行半透明置灰
    - 端口页重做（v0.4，tradis 机制）：TCP/UDP 双栏面板（蓝/橙徽章 + 计数 x/y + 独立滚动），每行：端口大数字 | 来源徽章（Container 绿 / Host 蓝）| 状态（使用中）| 服务/进程 | 备注 | 复制；备注复用 diancup 后端 `/api/ports/{port}/custom-name` 持久化
    - 品牌替换为 Docker Control

## 目录结构
```
panel/
├── docker-compose.yml    # 部署文件（安全加固版）
├── apply_skin.py         # v0.1 皮肤补丁（可重复执行）
├── apply_v2.py           # v0.2 纯 Docker 管理改造补丁（可重复执行）
└── static/               # 已打补丁的完整前端（覆盖挂载用）
```

## 部署（NAS / Linux 服务器）— 自建完整镜像
1. 前端已打入镜像 `ghcr.io/fujiwareshine/docker-control:latest`（GitHub Actions 自动构建，仓库 `.github/workflows/docker-build.yml`）
2. **首次拉取私有镜像需登录 GHCR**（在 NAS 上执行，PAT 需 `read:packages` 权限）：
   ```bash
   echo <你的GitHub PAT> | docker login ghcr.io -u FUJIWARESHINE --password-stdin
   ```
3. 编辑 `docker-compose.yml`：`DOCKER_DIR` 改为你机器的 Docker 配置目录
4. `docker compose up -d`，访问 `http://<IP>:9527`，默认密码 `admin@yjnas`
5. 登录后直达仪表板，容器状态监控即首屏

> 不想用自建镜像？把 compose 的 `image` 改回 `yjnas/diancup:latest` 并取消 `./static:/app/static` 挂载注释即可回到官方镜像+挂载覆盖模式。

## 网络模式与端口页功能（v0.4 补充）
| 方案 | 端口页效果 | 说明 |
|---|---|---|
| **host 网络**（compose 默认） | ✅ 完整（TCP/UDP 全端口 + Host/Container 来源） | 后端读宿主机 `/proc/net/tcp\|udp`；UI 直监听宿主机 9527 |
| bridge 网络 | 只显示容器端口映射 | 注释 `network_mode`、取消 `ports` 注释即可切换 |

host 模式仍保留 `cap_drop: ALL` + `no-new-privileges`，且**不需要** privileged / `/:/host`——端口扫描读 `/proc/net` 非特权操作。风险提示：容器内进程可访问宿主机 loopback 服务，部署前确认宿主机本地无裸奔无认证服务。

## 安全加固（相比 diancup 原版）
| 原版 | 现在 | 原因 |
|---|---|---|
| `privileged: true` | 移除，加 `cap_drop: ALL` + `no-new-privileges` | 管理 Docker 只需要 docker.sock，无需全部内核能力 |
| `/:/host` 挂载宿主机根目录 | 移除，只挂 `/var/run/docker.sock` | 纯 Docker 管理不读宿主机文件系统 |
| `network_mode: host` | bridge + `ports: 9527:9527` | 容器不暴露在宿主机网络栈 |

代价：主机监控/文件管理等功能不可用（UI 已同步移除）。若容器启动异常，优先排查 docker.sock 权限（`ls -l /var/run/docker.sock`）。

## 回退
- 回退 UI：去掉 compose 里的 `./static:/app/static` 挂载再 `up -d`，即恢复原版 diancup
- 各补丁首次执行时会在同目录生成 `.orig` 备份

## 修改皮肤 / 布局
- 色板集中在 `apply_skin.py` 的 `TRADIS_THEME` 和 `SKIN_CSS`
- 横条布局与操作按钮样式在 `apply_v2.py` 的 `DASHBOARD_CSS`，列表渲染逻辑在同文件 `NEW_RC`
- 改完重跑 `python apply_skin.py && python apply_v2.py`，服务器上重启容器即可

## 二次开发路线（下一步）
1. v0.3：起一个 sidecar 容器（Go/Node）挂 docker.sock，扩展 API（容器详情/终端/统计曲线），前端横条行点击展开详情
2. v0.4：参考 tradis 的部署引擎思路（plan → preflight → execute → verify）重写更新逻辑，逐步替换 diancup 后端
3. v0.5：多机管理（参考 tradis agent 模式）
