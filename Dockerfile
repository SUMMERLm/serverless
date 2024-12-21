FROM golang:1.20 as builder
ENV GOPROXY=https://goproxy.cn
ENV GO111MODULE=on \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
COPY go.mod go.mod
COPY go.sum go.sum
#RUN go mod download

# Copy the go source
COPY api/ api/
COPY cmd/ cmd/
COPY hack/ hack/
COPY pkg/ pkg/
COPY internal/ internal/
COPY vendor/ vendor/
#RUN apt-get install -y xz-utils 
#RUN apt-get update && apt-get install -y xz-utils && rm -rf /var/lib/apt/lists/*
#ADD https://github.com/upx/upx/releases/download/v3.96/upx-3.96-amd64_linux.tar.xz /usr/local
#RUN xz -d -c /usr/local/upx-3.96-amd64_linux.tar.xz | tar -xOf - upx-3.96-amd64_linux/upx > /bin/upx && chmod a+x /bin/upx
# Do an initial compilation before setting the version so that there is less to
# re-compile when the version changes
#RUN export https_proxy=http://192.168.97.36:7890 http_proxy=http://192.168.97.36:7890 all_proxy=socks5://192.168.97.36:7890
#RUN go build -mod=readonly "-ldflags=-s -w" ./...

ARG VERSION

# Compile all the binaries
RUN go build -v -o serverless cmd/serverless/main.go
RUN go build -v -o quota cmd/quota/main.go
RUN go build -v -o serverless_hpa cmd/hpa/main.go

#FROM scratch
#COPY --from=builder /workspace/serverless /
#COPY --from=builder /workspace/quota /
#COPY --from=builder /workspace/serverless_hpa /


#RUN upx serverless quota serverless_hpa

#
# IMAGE TARGETS
# -------------

FROM gengweifeng/gcr-io-distroless-static-nonroot:latest as serverless
WORKDIR /
COPY --from=builder /workspace/serverless .
USER 65532:65532
ENTRYPOINT ["/serverless"]

FROM gengweifeng/gcr-io-distroless-static-nonroot:latest as quota
WORKDIR /
COPY --from=builder /workspace/quota .
USER 65532:65532
ENTRYPOINT ["/quota"]

FROM gengweifeng/gcr-io-distroless-static-nonroot as serverless_hpa
WORKDIR /
COPY --from=builder /workspace/serverless_hpa .
USER 65532:65532
EXPOSE 8000
ENTRYPOINT ["/serverless_hpa"]
