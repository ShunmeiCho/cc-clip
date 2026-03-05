BINARY := cc-clip
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build test vet clean release-local

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/cc-clip/

test:
	go test ./... -count=1

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -rf dist/

release-local: clean
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%%/*}; \
		arch=$${platform##*/}; \
		output=dist/$(BINARY)-$${os}-$${arch}; \
		echo "Building $$platform..."; \
		GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o $$output ./cmd/cc-clip/; \
	done
	@echo "Binaries in dist/"
	@ls -lh dist/
