# =============================================================
# Dockerfile: "在吗" APP 后端服务
# 多阶段构建: 编译阶段 + 运行阶段 (最小化镜像体积)
# =============================================================

# --- 阶段 1: 编译 ---
FROM golang:1.26-alpine AS builder

WORKDIR /app

# 安装依赖 (缓存 go mod 层)
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并编译
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/zaima-server ./cmd/api

# --- 阶段 2: 运行 ---
FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

# 【安全】以非 root 用户运行
RUN adduser -D -u 1000 appuser

WORKDIR /app
COPY --from=builder --chown=appuser:appuser /app/zaima-server .
COPY --from=builder --chown=appuser:appuser /app/configs ./configs

# 自建文件存储目录 (供命名卷挂载, 首次创建卷时继承此属主)
RUN mkdir -p /app/data/uploads && chown -R appuser:appuser /app/data

USER appuser

EXPOSE 8080

CMD ["./zaima-server", "-config", "configs/config.docker.yaml"]
