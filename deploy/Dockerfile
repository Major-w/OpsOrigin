FROM golang:1.25-alpine AS builder

WORKDIR /app
# 复制全部仓库源码; 由仓库根 .dockerignore 排除 web-next/.git/.run/二进制等非源码
COPY . .
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.org

# cmd/core 是 go.work 工作区中的嵌套模块, 以工作区模式编译 ./cmd/core
RUN go mod download && CGO_ENABLED=0 go build -o /core ./cmd/core

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /core /usr/local/bin/core
EXPOSE 8080
ENTRYPOINT ["core"]
