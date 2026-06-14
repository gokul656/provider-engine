# ─────────────────────────────────────────────
# DCP Makefile
# Usage:
#   make cp        — setup + boot control plane
#   make provider  — setup + boot provider
#   make health    — run health checks
#   make stop      — stop all services
#   make clean     — remove installed binaries
# ─────────────────────────────────────────────

.PHONY: cp provider health stop clean check-root

SHELL := /bin/bash
SUDO  := sudo

# ── Enforce root for privileged targets ───────
check-sudo:
	@if ! sudo -v 2>/dev/null; then \
		echo "ERROR: sudo access required"; \
		exit 1; \
	fi

# ── Control Plane ─────────────────────────────
cp: check-sudo
	@echo "==> [CP] Base setup..."
	@$(SUDO) bash scripts/base.sh
	@echo "==> [CP] SPIRE..."
	@$(SUDO) bash scripts/sprie.sh
	@echo "==> [CP] WireGuard..."
	@$(SUDO) bash scripts/wireguard.sh || true
	@echo "==> [CP] Control plane services..."
	@$(SUDO) bash scripts/control-plane.sh
	@echo "✓ Control plane ready"

# ── Provider ──────────────────────────────────
provider: check-sudo
	@echo "==> [PROVIDER] Base setup..."
	@$(SUDO) bash scripts/base.sh
	@echo "==> [PROVIDER] SPIRE agent..."
	@$(SUDO) bash scripts/sprie.sh
	@echo "==> [PROVIDER] WireGuard..."
	@$(SUDO) bash scripts/wireguard.sh || true
	@echo "==> [PROVIDER] Firecracker + Jailer..."
	@$(SUDO) bash scripts/firecracker.sh
	@echo "==> [PROVIDER] Provider agent..."
	@$(SUDO) bash scripts/provider-agent.sh
	@echo "✓ Provider ready"

# ── Health Checks (no sudo needed) ────────────
health:
	@echo ""
	@echo "[ ntp ]"          ; chronyc tracking | grep "System time"           || echo "FAIL"
	@echo "[ wireguard ]"    ; $(SUDO) wg show                                 || echo "FAIL"
	@echo "[ spire-server ]" ; $(SUDO) spire-server healthcheck \
	                            -socketPath /tmp/spire-server/private/api.sock  || echo "FAIL"
	@echo "[ spire-agent ]"  ; $(SUDO) spire-agent healthcheck \
	                            -socketPath /tmp/spire-agent/public/api.sock    || echo "FAIL"
	@echo "[ redis ]"        ; redis-cli ping                                   || echo "FAIL"
	@echo "[ postgresql ]"   ; pg_isready                                       || echo "FAIL"
	@echo "[ firecracker ]"  ; firecracker --version                            || echo "FAIL"
	@echo "[ kvm ]"          ; ls /dev/kvm && echo "available"                  || echo "NOT available"
	@echo ""

# ── Stop All ──────────────────────────────────
stop: check-sudo
	@echo "==> Stopping all DCP services..."
	@$(SUDO) systemctl stop \
		dcp-auth dcp-registry dcp-cert-issuer dcp-provider \
		spire-agent spire-server \
		wg-quick@wg0 \
		redis postgresql chrony 2>/dev/null || true
	@echo "✓ All services stopped"

# ── Clean ─────────────────────────────────────
clean: check-sudo
	@echo "==> Removing binaries..."
	@$(SUDO) rm -f /usr/local/bin/spire-server \
	               /usr/local/bin/spire-agent \
	               /usr/local/bin/firecracker \
	               /usr/local/bin/jailer
	@$(SUDO) rm -rf /opt/spire /opt/dcp/kernels
	@echo "✓ Clean complete"
