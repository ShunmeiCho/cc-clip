BINARY := cc-clip
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build test vet clean release-local

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/cc-clip/
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		codesign --force --sign - --identifier com.cc-clip.cli $(BINARY); \
	fi

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
		if [ "$$os" = "windows" ]; then output="$${output}.exe"; fi; \
		echo "Building $$platform..."; \
		GOOS=$$os GOARCH=$$arch go build $(LDFLAGS) -o $$output ./cmd/cc-clip/; \
		if [ "$$os" = "darwin" ] && [ "$$(uname -s)" = "Darwin" ]; then \
			echo "  Signing $$output..."; \
			codesign --force --sign - --identifier com.cc-clip.cli $$output; \
		fi; \
	done
	@echo "Binaries in dist/"
	@ls -lh dist/
