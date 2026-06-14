BINARY  := dcp-cp
LDFLAGS := -s -w
OUT     := dist

.PHONY: all build linux-amd64 linux-arm64 release clean run token

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(OUT)/$(BINARY)-linux-amd64 .

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(OUT)/$(BINARY)-linux-arm64 .

release: linux-amd64 linux-arm64
	@echo "Built:"
	@ls -lh $(OUT)/

clean:
	rm -rf $(BINARY) $(OUT)/

# Generate a provider registration token
token:
	./$(BINARY) token generate

# Run locally (needs Postgres + Redis running)
run: build
	./$(BINARY) serve \
	  --jwt-secret $${DCP_JWT_SECRET} \
	  --addr $${DCP_LISTEN:-:8080} \
	  --redis $${DCP_REDIS:-redis://localhost:6379} \
	  --pg $${DCP_PG_DSN:-postgres://dcp:dcp@localhost/dcp_registry?sslmode=disable}
