BIN     := inspector_claude
PKG     := .
DIST    := dist
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
# -trimpath scrubs local filesystem paths (e.g. /Users/<you>/...) from the
# binary, so a shared/airdropped executable leaks no username or build paths.
BUILDFLAGS := -trimpath -ldflags "$(LDFLAGS)"

# Pure-Go (CGO off) => trivially cross-compilable, static single binary.
export CGO_ENABLED = 0

.PHONY: build install run vet fmt clean cross

build: ## build for the host platform into ./bin
	go build $(BUILDFLAGS) -o bin/$(BIN) $(PKG)

install: ## go install into $GOBIN / $GOPATH/bin
	go install $(BUILDFLAGS) $(PKG)

run: ## run the TUI
	go run $(PKG)

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin $(DIST)

# --- cross compilation -----------------------------------------------------
# make cross  -> builds all targets below into ./dist
PLATFORMS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64

cross: ## build every target in PLATFORMS into ./dist
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=$(DIST)/$(BIN)-$$os-$$arch$$ext; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch go build $(BUILDFLAGS) -o $$out $(PKG) || exit 1; \
	done
	@echo "done -> $(DIST)/"
