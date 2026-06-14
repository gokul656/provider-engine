#!/usr/bin/env bash
# health.sh — Quick health check for all DCP services
set -euo pipefail

pass() { printf "  %-22s \e[32m✓\e[0m %s\n" "$1" "$2"; }
fail() { printf "  %-22s \e[31m✗\e[0m %s\n" "$1" "$2"; }
info() { printf "  %-22s   %s\n" "$1" "$2"; }

echo ""
echo "DCP Service Health Check"
echo "────────────────────────────────────────────"

# NTP
if chronyc tracking 2>/dev/null | grep -q "System time"; then
  DRIFT=$(chronyc tracking 2>/dev/null | grep "System time" | awk '{print $4, $5}')
  pass "chrony" "drift: $DRIFT"
else
  fail "chrony" "not running or no sync"
fi

# WireGuard
if sudo wg show wg0 &>/dev/null; then
  PEERS=$(sudo wg show wg0 peers 2>/dev/null | wc -l)
  pass "wireguard (wg0)" "$PEERS peer(s)"
else
  info "wireguard (wg0)" "not up"
fi

# SPIRE server
if spire-server healthcheck -socketPath /tmp/spire-server/private/api.sock &>/dev/null; then
  pass "spire-server" "healthy"
else
  info "spire-server" "not running (CP only)"
fi

# SPIRE agent
if spire-agent healthcheck -socketPath /tmp/spire-agent/public/api.sock &>/dev/null; then
  pass "spire-agent" "healthy"
else
  info "spire-agent" "not running"
fi

# Redis
if redis-cli ping 2>/dev/null | grep -q PONG; then
  pass "redis" "PONG"
else
  info "redis" "not running (CP only)"
fi

# PostgreSQL
if pg_isready 2>/dev/null | grep -q "accepting"; then
  pass "postgresql" "accepting connections"
else
  info "postgresql" "not running (CP only)"
fi

# Firecracker
if command -v firecracker &>/dev/null; then
  pass "firecracker" "$(firecracker --version 2>/dev/null | head -1)"
else
  info "firecracker" "not installed (CP only)"
fi

# KVM
if [[ -e /dev/kvm ]]; then
  pass "kvm" "available"
else
  info "kvm" "not available — provider uses container fallback"
fi

# dcp-cp service
if systemctl is-active dcp-cp &>/dev/null; then
  pass "dcp-cp" "running"
else
  info "dcp-cp" "not running"
fi

# dcp-provider service
if systemctl is-active dcp-provider &>/dev/null; then
  pass "dcp-provider" "running"
else
  info "dcp-provider" "not running"
fi

echo "────────────────────────────────────────────"
echo ""
