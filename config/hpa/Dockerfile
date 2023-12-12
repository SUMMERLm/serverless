# 使用官方提供的 Go 开发镜像作为基础镜像
FROM golang:1.19.8
ENV GO111MODULE=on \
    CGO_ENABLED=1 \
    GOOS=linux \
    GOARCH=amd64
# 切换工作目录为 /demo
WORKDIR /serverless_hpa

# 将当前目录下的内容复制到 /demo
ADD . /serverless_hpa

# 开启 GO MOD, 用于安装 HTTP 需要的 Gin 依赖
RUN go env -w GO111MODULE=on &&\
    go env -w GOPROXY=https://goproxy.cn,direct



ENV GOPROXY="https://goproxy.io"
RUN go build -o serverless_hpa .
# 允许外接访问容器的 80 端口
EXPOSE 8000
# 运行这个容器中的 HTTP 实例
ENTRYPOINT  ["./serverless_hpa"]