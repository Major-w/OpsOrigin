# Use the go toolchain available on PATH (do not hardcode machine-specific
# GOROOT/GOPATH, which breaks `make` on other developers' machines).
GO := go
GOPROXY := https://goproxy.cn,direct

BINDIR := bin

.PHONY: all build clean test lint fmt tidy

all: build

build: build-core build-proxy build-agent

build-core:
	GOPROXY=$(GOPROXY) $(GO) build -o $(BINDIR)/core ./cmd/core

build-proxy:
	GOPROXY=$(GOPROXY) $(GO) build -o $(BINDIR)/proxy ./cmd/proxy

build-agent:
	GOPROXY=$(GOPROXY) $(GO) build -o $(BINDIR)/agent ./cmd/agent

test:
	GOPROXY=$(GOPROXY) $(GO) test ./...

lint:
	GOPROXY=$(GOPROXY) $(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	GOPROXY=$(GOPROXY) $(GO) mod tidy
	GOPROXY=$(GOPROXY) $(GO) work sync

clean:
	rm -rf $(BINDIR) core proxy agent
