BINARY := rtunnel
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: all build clean test lint update

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

# Update: pull latest, rebuild, reinstall binary, restart service (preserves config)
update:
	git pull
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY) .
	@if [ -f /etc/systemd/system/rtunnel-server.service ]; then \
		echo "==> Updating server binary..."; \
		sudo install -m 0755 bin/$(BINARY) /usr/local/bin/$(BINARY); \
		sudo systemctl restart rtunnel-server; \
		echo "==> Server updated and restarted"; \
	elif [ -f /etc/systemd/system/rtunnel-client.service ]; then \
		echo "==> Updating client binary (Linux)..."; \
		sudo install -m 0755 bin/$(BINARY) /usr/local/bin/$(BINARY); \
		sudo systemctl restart rtunnel-client; \
		echo "==> Client updated and restarted"; \
	elif [ -f /Library/LaunchDaemons/com.rtunnel.client.plist ]; then \
		echo "==> Updating client binary (macOS)..."; \
		sudo install -m 0755 bin/$(BINARY) /usr/local/bin/$(BINARY); \
		sudo launchctl bootout system /Library/LaunchDaemons/com.rtunnel.client.plist 2>/dev/null || true; \
		sudo launchctl bootstrap system /Library/LaunchDaemons/com.rtunnel.client.plist; \
		echo "==> Client updated and restarted"; \
	else \
		echo "No rtunnel service found. Run install-server.sh or install-client.sh first."; \
		exit 1; \
	fi
