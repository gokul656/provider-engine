#!/usr/bin/env bash
# install.sh — Install OS packages and third-party binaries (Firecracker, SPIRE)
# This script does NOT configure anything — all configuration is done by:
#   dcp-cp setup       (control plane)
#   dcp-provider setup (provider)
#
# Usage:
#   sudo ./install.sh cp            — control plane machine
#   sudo ./install.sh provider      — provider machine
#   sudo ./install.sh both          — single machine (dev / single-node)

set -euo pipefail

MODE="${1:-}"
if [[ "$MODE" != "cp" && "$MODE" != "provider" && "$MODE" != "both" ]]; then
  echo "Usage: sudo $0 <cp|provider|both>"
  exit 1
fi

[[ $EUID -ne 0 ]] && exec sudo bash "$0" "$@"

is_cp()       { [[ "$MODE" == "cp"       || "$MODE" == "both" ]]; }
is_provider() { [[ "$MODE" == "provider" || "$MODE" == "both" ]]; }

SPIRE_VERSION="1.9.4"

log()  { echo ""; echo "==> $*"; }
ok()   { echo "    ✓ $*"; }
skip() { echo "    — $* (already installed)"; }

# ── 1. Base packages (both) ───────────────────────────────────────────────────
log "Base packages"
apt-get update -qq
apt-get install -y -qq \
  curl wget git \
  build-essential \
  apt-transport-https ca-certificates gnupg lsb-release \
  jq unzip \
  ufw fail2ban \
  chrony \
  iproute2 iptables \
  openssh-server \
  wireguard wireguard-tools \
  openssl
ok "base packages"

# NTP — required for SPIRE short-lived SVIDs
systemctl enable chrony --now
ok "chrony"

# ── 2. Control plane packages (Redis + PostgreSQL) ────────────────────────────
if is_cp; then
  log "Control plane packages (Redis, PostgreSQL)"
  apt-get install -y -qq redis-server postgresql postgresql-contrib
  # Bind Redis to localhost only
  sed -i 's/^bind .*/bind 127.0.0.1/' /etc/redis/redis.conf
  systemctl enable redis-server postgresql --now
  ok "redis + postgresql"
fi

# ── 3. Provider packages (Firecracker deps + Docker fallback) ─────────────────
if is_provider; then
  log "Provider packages"
  apt-get install -y -qq \
    libseccomp-dev libseccomp2 \
    cryptsetup \
    qemu-utils \
    docker.io

  systemctl enable docker --now
  usermod -aG kvm,docker "${SUDO_USER:-$USER}" 2>/dev/null || true
  ok "provider packages"

  # Firecracker + Jailer
  if ! command -v firecracker &>/dev/null; then
    log "Firecracker"
    ARCH="$(uname -m)"
    FC_VER=$(curl -sf "https://api.github.com/repos/firecracker-microvm/firecracker/releases/latest" \
      | jq -r .tag_name)
    TGZ="/tmp/fc.tgz"
    curl -fsSL -o "$TGZ" \
      "https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VER}/firecracker-${FC_VER}-${ARCH}.tgz"
    tar -xzf "$TGZ" -C /tmp
    RDIR="/tmp/release-${FC_VER}-${ARCH}"
    install -m 0755 "${RDIR}/firecracker-${FC_VER}-${ARCH}" /usr/local/bin/firecracker
    install -m 0755 "${RDIR}/jailer-${FC_VER}-${ARCH}"      /usr/local/bin/jailer
    rm -rf "$TGZ" "$RDIR"
    ok "firecracker $(firecracker --version | head -1)"
  else
    skip "firecracker"
  fi

  # Kernel for Firecracker microVMs — path matches firecracker.go: kernelPath
  KERNEL_PATH="/opt/dcp/kernels/vmlinux"
  mkdir -p "$(dirname "$KERNEL_PATH")"
  if [[ ! -f "$KERNEL_PATH" ]]; then
    ARCH="$(uname -m)"
    curl -fsSL -o "$KERNEL_PATH" \
      "https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/${ARCH}/kernels/vmlinux.bin"
    ok "FC kernel → $KERNEL_PATH"
  else
    skip "FC kernel"
  fi

  # VM directories — match firecracker.go: vmBaseDir
  mkdir -p /var/lib/dcp/vms /var/lib/dcp/rootfs
  ok "VM directories"
fi

# ── 4. SPIRE binaries (both) ──────────────────────────────────────────────────
if ! command -v spire-server &>/dev/null; then
  log "SPIRE ${SPIRE_VERSION}"
  curl -fsSL -o /tmp/spire.tar.gz \
    "https://github.com/spiffe/spire/releases/download/v${SPIRE_VERSION}/spire-${SPIRE_VERSION}-linux-amd64-musl.tar.gz"
  tar -xzf /tmp/spire.tar.gz -C /tmp
  mv "/tmp/spire-${SPIRE_VERSION}" /opt/spire
  ln -sf /opt/spire/bin/spire-server /usr/local/bin/spire-server
  ln -sf /opt/spire/bin/spire-agent  /usr/local/bin/spire-agent
  rm -f /tmp/spire.tar.gz
  ok "SPIRE ${SPIRE_VERSION}"
else
  skip "SPIRE"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo "════════════════════════════════════════════════════"
echo " Packages installed. Now run the Go CLI to configure:"
echo ""

if is_cp; then
  echo "   sudo dcp-cp setup --wg-endpoint <PUBLIC_IP>:51820"
  echo ""
fi
if is_provider; then
  echo "   sudo dcp-provider setup \\"
  echo "     --cp-url https://<CP_IP>:8080 \\"
  echo "     --token  <from: dcp-cp token generate> \\"
  echo "     --spire-token <from: dcp-cp token generate --spire>"
  echo ""
fi
echo "════════════════════════════════════════════════════"
