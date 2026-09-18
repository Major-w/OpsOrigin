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
# font-noto-cjk: pdf_generate 内嵌 CJK 字体支持中文(纯 Go 手写 PDF 运行时探测并嵌入)
RUN apk add --no-cache ca-certificates tzdata pandoc font-noto-cjk
# pdf_generate 默认探测路径不含 Noto, 用环境变量指向已安装的 CJK 字体(集合取首个可解析的字体)
ENV OPSORIGIN_PDF_FONT=/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc
COPY --from=builder /core /usr/local/bin/core
# 规范工作目录(cwd 不依赖任何挂载; 持久配置存 PostgreSQL, 非 .env)
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["core"]
