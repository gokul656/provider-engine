BINARY   := dcp-provider
MODULE   := github.com/dcp/provider-agent
LDFLAGS  := -s -w
OUT      := dist

.PHONY: all build linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 clean

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

# ── Cross-compile targets ─────────────────────────────────────────────────────
linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(OUT)/$(BINARY)-linux-amd64 .

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(OUT)/$(BINARY)-linux-arm64 .

darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(OUT)/$(BINARY)-darwin-amd64 .

darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(OUT)/$(BINARY)-darwin-arm64 .

release: linux-amd64 linux-arm64 darwin-amd64 darwin-arm64
	@echo "Built all targets:"
	@ls -lh $(OUT)/

clean:
	rm -rf $(BINARY) $(OUT)/

# ── Run locally (dev) ─────────────────────────────────────────────────────────
run:
	go run . --token $${DCP_TOKEN} --cp-url $${DCP_CP_URL:-https://api.dcp.internal}
