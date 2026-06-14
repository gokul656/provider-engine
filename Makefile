# ─────────────────────────────────────────────────────────────────────────────
# DCP — top-level Makefile
# ─────────────────────────────────────────────────────────────────────────────
.PHONY: build build-cp build-provider release install-cp install-provider \
        install-both health clean

SHELL := /bin/bash

# ── Build ─────────────────────────────────────────────────────────────────────

build: build-cp build-provider

build-cp:
	@echo "==> Building dcp-cp..."
	@$(MAKE) -C dcp-control-plane build
	@echo "✓ dcp-cp ready at dcp-control-plane/dcp-cp"

build-provider:
	@echo "==> Building dcp-provider..."
	@$(MAKE) -C dcp-provider build
	@echo "✓ dcp-provider ready at dcp-provider/dcp-provider"

# Cross-compile for Linux (deploy target)
release:
	@echo "==> Cross-compiling for Linux..."
	@$(MAKE) -C dcp-control-plane linux-amd64 linux-arm64
	@$(MAKE) -C dcp-provider     linux-amd64 linux-arm64
	@echo ""
	@echo "Artifacts:"
	@ls -lh dcp-control-plane/dist/ dcp-provider/dist/

# ── Install (run on Ubuntu target machine) ────────────────────────────────────

install-cp:
	@sudo bash scripts/install.sh cp

install-provider:
	@sudo bash scripts/install.sh provider

install-both:
	@sudo bash scripts/install.sh both

# ── Deploy binaries to a remote machine ──────────────────────────────────────
# Usage: make deploy-cp HOST=user@1.2.3.4

deploy-cp: release
	@test -n "$(HOST)" || (echo "Usage: make deploy-cp HOST=user@1.2.3.4"; exit 1)
	scp dcp-control-plane/dist/dcp-cp-linux-amd64 $(HOST):/usr/local/bin/dcp-cp
	ssh $(HOST) "chmod +x /usr/local/bin/dcp-cp && systemctl restart dcp-cp"
	@echo "✓ dcp-cp deployed to $(HOST)"

deploy-provider: release
	@test -n "$(HOST)" || (echo "Usage: make deploy-provider HOST=user@1.2.3.4"; exit 1)
	scp dcp-provider/dist/dcp-provider-linux-amd64 $(HOST):/usr/local/bin/dcp-provider
	ssh $(HOST) "chmod +x /usr/local/bin/dcp-provider && systemctl restart dcp-provider"
	@echo "✓ dcp-provider deployed to $(HOST)"

# ── Health ────────────────────────────────────────────────────────────────────

health:
	@sudo bash scripts/health.sh

# ── Clean ─────────────────────────────────────────────────────────────────────

clean:
	@$(MAKE) -C dcp-control-plane clean
	@$(MAKE) -C dcp-provider clean
