.PHONY: build build-scheme2 test test-scheme2 vet docker clean

GO ?= go
BIN := bin
IMAGE ?= grokbuild2api:latest

build:
	@mkdir -p $(BIN)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/poolctl ./cmd/poolctl
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/pool-proxy ./cmd/pool-proxy
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/render-config ./cmd/render-config

# Scheme 2 multi-process skeleton (gateway/worker/controlplane/refresher)
build-scheme2:
	@mkdir -p $(BIN)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/gateway ./cmd/gateway
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/worker ./cmd/worker
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/controlplane ./cmd/controlplane
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/refresher ./cmd/refresher

test:
	$(GO) test ./...

test-scheme2:
	$(GO) test ./internal/store/... ./internal/clusterstate/... ./internal/controlplane/... ./internal/gateway/... ./internal/worker/... ./internal/refresher/...

vet:
	$(GO) vet ./...

docker:
	docker build -t $(IMAGE) .

clean:
	rm -rf $(BIN)
