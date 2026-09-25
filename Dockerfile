# ============ 构建阶段：编译自研 Go 后端 ============
FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/docker-control .

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
