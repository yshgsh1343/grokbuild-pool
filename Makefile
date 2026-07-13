.PHONY: build docker clean

GO ?= go
BIN := bin
IMAGE ?= grokbuild2api:latest

build:
	@mkdir -p $(BIN)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/poolctl ./cmd/poolctl
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN)/pool-proxy ./cmd/pool-proxy

docker:
	docker build -t $(IMAGE) .

clean:
	rm -rf $(BIN)
