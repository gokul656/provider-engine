#!/bin/bash
set -euo pipefail

# Self-escalate if not root
if [[ $EUID -ne 0 ]]; then
  exec sudo bash "$0" "$@"
fi

case "${1:-}" in

  cp)
    echo "==> Booting Control Plane..."
    sudo bash scripts/base.sh          # chrony + apt deps
    sudo bash scripts/sprie.sh         # spire-server + spire-agent
    sudo bash scripts/wireguard.sh     # wg-quick@wg0
    sudo bash scripts/control-plane.sh # postgresql + redis + FastAPI services
    ;;

  provider)
    echo "==> Booting Provider Machine..."
    sudo bash scripts/base.sh          # chrony + apt deps
    sudo bash scripts/sprie.sh         # spire-agent (connects to CP)
    sudo bash scripts/wireguard.sh     # wg-quick@wg0
    sudo bash scripts/firecracker.sh   # firecracker + jailer + vmlinux
    sudo bash scripts/provider-agent.sh # dcp-provider
    ;;

  health)
    echo "[ ntp ]"         && chronyc tracking | grep "System time"
    echo "[ wireguard ]"   && sudo wg show
    echo "[ spire-server ]" && spire-server healthcheck \
                                -socketPath /tmp/spire-server/private/api.sock
    echo "[ spire-agent ]" && spire-agent healthcheck \
                                -socketPath /tmp/spire-agent/public/api.sock
    echo "[ redis ]"       && redis-cli ping
    echo "[ postgresql ]"  && pg_isready
    echo "[ firecracker ]" && firecracker --version
    echo "[ kvm ]"         && ls /dev/kvm && echo "available" || echo "NOT available"
    ;;

  stop)
    sudo systemctl stop dcp-provider dcp-auth dcp-registry dcp-cert-issuer \
                        spire-agent wg-quick@wg0 spire-server \
                        redis postgresql chrony 2>/dev/null || true
    echo "All services stopped"
    ;;

  *)
    echo "Usage: $0 {cp|provider|health|stop}"
    exit 1
    ;;
esac