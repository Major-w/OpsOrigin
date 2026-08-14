# Use the go toolchain available on PATH (do not hardcode machine-specific
# GOROOT/GOPATH, which breaks `make` on other developers' machines).
GO := go
GOPROXY := https://goproxy.cn,direct

BINDIR := bin

# go.work 聚合的模块列表（根模块 + cmd 下四个独立子模块）。
# ./... 只覆盖当前目录所在模块，因此构建 / 测试 / vet 需逐个模块执行。
MODULES := . ./cmd/agent ./cmd/core ./cmd/k8s-probe ./cmd/proxy

.PHONY: all build build-core build-proxy build-agent build-k8s-probe test lint fmt tidy generate clean

all: build

build: build-core build-proxy build-agent build-k8s-probe

build-core:
	GOPROXY=$(GOPROXY) $(GO) build -o $(BINDIR)/core ./cmd/core

build-proxy:
	GOPROXY=$(GOPROXY) $(GO) build -o $(BINDIR)/proxy ./cmd/proxy

build-agent:
	GOPROXY=$(GOPROXY) $(GO) build -o $(BINDIR)/agent ./cmd/agent

build-k8s-probe:
	GOPROXY=$(GOPROXY) $(GO) build -o $(BINDIR)/k8s-probe ./cmd/k8s-probe

# 逐个模块执行，避免 cmd/* 子模块的测试被 ./... 静默跳过。
test:
	@for m in $(MODULES); do \
		echo "==> go test $$m"; \
		GOPROXY=$(GOPROXY) $(GO) test -C "$$m" ./... || exit 1; \
	done

lint:
	@for m in $(MODULES); do \
		echo "==> go vet $$m"; \
		GOPROXY=$(GOPROXY) $(GO) vet -C "$$m" ./... || exit 1; \
	done

fmt:
	@for m in $(MODULES); do \
		$(GO) fmt -C "$$m" ./...; \
	done

tidy:
	GOPROXY=$(GOPROXY) $(GO) mod tidy
	GOPROXY=$(GOPROXY) $(GO) work sync

generate:
	$(GO) generate ./internal/ent

clean:
	rm -rf $(BINDIR) core proxy agent k8s-probe
