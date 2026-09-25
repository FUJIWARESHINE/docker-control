# ============ 构建阶段：编译自有后端 ============
FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY backend/go.mod backend/go.sum ./
RUN go mod download || true
COPY backend/ .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/docker-control .

# ============ 运行阶段：纯净 alpine + 自有后端 + 补丁前端 ============
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 dcuser 2>/dev/null || true

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

CMD ["/app/docker-control"]
