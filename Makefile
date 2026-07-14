.PHONY: build build-go build-scheme2 frontend-build test test-scheme2 vet docker clean

GO ?= go
BIN := bin
IMAGE ?= grokbuild2api:latest
PNPM ?= pnpm

# Default build: React admin UI then Go binaries
build: frontend-build build-go

frontend-build:
	cd frontend && $(PNPM) install --frozen-lockfile
	cd frontend && $(PNPM) build
	rm -rf internal/adminui/dist
	mkdir -p internal/adminui/dist
	cp -a frontend/dist/. internal/adminui/dist/

build-go:
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
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/seed-sqlite ./cmd/seed-sqlite

test:
	$(GO) test ./...

test-scheme2:
	$(GO) test ./internal/store/... ./internal/clusterstate/... ./internal/controlplane/... ./internal/gateway/... ./internal/worker/... ./internal/refresher/...

vet:
	$(GO) vet ./...

docker:
	docker build -t $(IMAGE) .

clean:
	rm -rf $(BIN) frontend/dist
