# go.work 声明 go >= 1.26.6; 官方镜像默认 GOTOOLCHAIN=local 不会自动升级,
# 改为 auto 让 go 按 go.work 要求经 GOPROXY 下载对应工具链(无需更换基础镜像)。
FROM golang:1.25-alpine AS builder

WORKDIR /app
# 复制全部仓库源码; 由仓库根 .dockerignore 排除 web/.git/.run/二进制等非源码
COPY . .
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.org
ENV GOTOOLCHAIN=auto

# cmd/core 是 go.work 工作区中的嵌套模块, 以工作区模式编译 ./cmd/core
RUN go mod download && CGO_ENABLED=0 go build -o /core ./cmd/core

FROM alpine:3.21
# pandoc: AI 文档导出服务端通道(md→docx/pptx 确定性转换, 见 internal/ai/export_doc.go)
RUN apk add --no-cache ca-certificates tzdata pandoc
COPY --from=builder /core /usr/local/bin/core
# 规范工作目录(cwd 不依赖任何挂载; 持久配置存 PostgreSQL, 非 .env)
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["core"]
