# ============ 构建阶段：编译自研 Go 后端 ============
FROM golang:1.23-alpine AS builder

# VERSION 由 CI 通过 --build-arg 注入（取自 git tag）。
# 本地直接 docker build 时不传则回落到 dev，不影响构建。
ARG VERSION=dev

WORKDIR /build
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
# 用 -X 把版本号写进 main.Version 变量（需 main.go 里把 Version 改成 var）
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags="-s -w -X main.Version=${VERSION}" \
      -o /out/docker-control .

# ============ 运行阶段：纯净 alpine ============
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /out/docker-control ./docker-control
COPY static/ ./static/

ENV PORT=9527 \
    DATA_DIR=/app/data \
    STATIC_DIR=/app/static \
    DOCKER_HOST=unix:///var/run/docker.sock \
    TZ=Asia/Shanghai

VOLUME ["/app/data"]
EXPOSE 9527

# 必须以 root 运行：docker.sock 属主为 root，非 root 无法访问（面板的核心能力依赖）
CMD ["/app/docker-control"]
