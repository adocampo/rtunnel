BINARY := rtunnel
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: all build clean test lint

all: build

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY) .

# Cross-compile for all platforms
release:
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*} GOARCH=$${platform#*/} CGO_ENABLED=0 \
		go build $(LDFLAGS) -o bin/$(BINARY)-$${platform%/*}-$${platform#*/}$$([ "$${platform%/*}" = "windows" ] && echo ".exe") . ; \
		echo "Built: bin/$(BINARY)-$${platform%/*}-$${platform#*/}" ; \
	done

test:
	go test -v -race ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

# Docker image for the client (minimal, static binary)
docker:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-amd64 .
	docker build -t $(BINARY):$(VERSION) .

# Install locally
install: build
	cp bin/$(BINARY) $(GOPATH)/bin/$(BINARY)
