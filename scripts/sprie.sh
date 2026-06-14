#!/bin/bash
set -euo pipefail

# Self-escalate if not root
if [[ $EUID -ne 0 ]]; then
  exec sudo bash "$0" "$@"
fi

SPIRE_VER="1.14.4"
SPIRE_DIR="/opt/spire"
TRUST_DOMAIN="dcp.io"
CP_IP="${1:-127.0.0.1}"   # pass your control-plane IP as arg: ./setup-spire.sh 10.0.0.1

# ─────────────────────────────────────────────
# 1. Download + Install
# ─────────────────────────────────────────────
echo "==> [1/6] Downloading SPIRE ${SPIRE_VER}..."
curl -Lo /tmp/spire.tar.gz \
  "https://github.com/spiffe/spire/releases/download/v${SPIRE_VER}/spire-${SPIRE_VER}-linux-amd64-musl.tar.gz"

echo "==> [2/6] Extracting..."
rm -rf /tmp/spire-${SPIRE_VER}
tar -xzf /tmp/spire.tar.gz -C /tmp

echo "==> [3/6] Installing to ${SPIRE_DIR}..."
sudo rm -rf "${SPIRE_DIR}"
sudo mkdir -p "${SPIRE_DIR}"
sudo cp -r /tmp/spire-${SPIRE_VER}/. "${SPIRE_DIR}/"
sudo rm -rf /tmp/spire-${SPIRE_VER} /tmp/spire.tar.gz

echo "==> Symlinking binaries..."
sudo ln -sf "${SPIRE_DIR}/bin/spire-server" /usr/local/bin/spire-server
sudo ln -sf "${SPIRE_DIR}/bin/spire-agent"  /usr/local/bin/spire-agent

# ─────────────────────────────────────────────
# 2. Server Config
# ─────────────────────────────────────────────
echo "==> [4/6] Writing server config..."
sudo mkdir -p /etc/spire /var/lib/spire/server /var/lib/spire/agent /tmp/spire-agent/public

sudo chmod 755 /etc/spire

sudo cat > /etc/spire/server.conf <<EOF
server {
  bind_address          = "0.0.0.0"
  bind_port             = "8081"
  trust_domain          = "${TRUST_DOMAIN}"
  data_dir              = "/var/lib/spire/server"
  log_level             = "INFO"
  default_x509_svid_ttl = "1h"
  ca_ttl                = "24h"
}
plugins {
  DataStore "sql" {
    plugin_data {
      database_type     = "sqlite3"
      connection_string = "/var/lib/spire/server/datastore.sqlite3"
    }
  }
  KeyManager "disk" {
    plugin_data {
      keys_path = "/var/lib/spire/server/keys.json"
    }
  }
  NodeAttestor "join_token" {}
}
EOF

# ─────────────────────────────────────────────
# 3. Server systemd
# ─────────────────────────────────────────────
sudo cat > /etc/systemd/system/spire-server.service <<'EOF'
[Unit]
Description=SPIRE Server
After=network.target

[Service]
ExecStart=/usr/local/bin/spire-server run -config /etc/spire/server.conf
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=spire-server

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now spire-server

# ── wait for server to be ready ──────────────
echo "==> Waiting for SPIRE server to be ready..."
for i in {1..15}; do
  if spire-server healthcheck -socketPath /tmp/spire-server/private/api.sock &>/dev/null; then
    echo "    Server is healthy (attempt ${i})"
    break
  fi
  if [ "$i" -eq 15 ]; then
    echo "ERROR: Server failed to start after 30s. Check logs:"
    echo "       journalctl -u spire-server --no-pager -n 50"
    exit 1
  fi
  echo "    ... waiting (${i}/15)"
  sleep 2
done

# ─────────────────────────────────────────────
# 4. Register workload identities
# ─────────────────────────────────────────────
echo "==> [5/6] Registering service identities..."

for SVC in api-gateway auth-service registry cert-issuer; do
  SPIFFE_ID="spiffe://${TRUST_DOMAIN}/${SVC}"

  # Check if entry already exists
  EXISTING=$(spire-server entry show \
    -socketPath /tmp/spire-server/private/api.sock \
    -spiffeID "${SPIFFE_ID}" 2>/dev/null | grep "Entry ID" | awk '{print $NF}')

  if [ -n "${EXISTING}" ]; then
    echo "    Skipping ${SVC} — entry already exists (${EXISTING})"
    continue
  fi

  spire-server entry create \
    -socketPath /tmp/spire-server/private/api.sock \
    -spiffeID  "${SPIFFE_ID}" \
    -parentID  "spiffe://${TRUST_DOMAIN}/node/control-plane" \
    -selector  "unix:user:${SVC}"

  echo "    Registered: ${SPIFFE_ID}"
done

# open firewall port
ufw allow 8081/tcp || true
# ─────────────────────────────────────────────
# 5. Bootstrap bundle + join token
# ─────────────────────────────────────────────
echo "==> Fetching bootstrap bundle from server..."
spire-server bundle show \
  -socketPath /tmp/spire-server/private/api.sock \
  -format pem > /etc/spire/bootstrap.crt

echo "==> Generating join token..."
JOIN_TOKEN=$(spire-server token generate \
  -socketPath /tmp/spire-server/private/api.sock \
  -spiffeID spiffe://${TRUST_DOMAIN}/node/control-plane | awk '{print $2}')
echo "    Token: ${JOIN_TOKEN}"

# ─────────────────────────────────────────────
# 6. Agent Config + systemd
# ─────────────────────────────────────────────
echo "==> [6/6] Writing agent config..."

cat > /etc/spire/agent.conf <<EOF
agent {
  data_dir          = "/var/lib/spire/agent"
  log_level         = "INFO"
  server_address    = "${CP_IP}"
  server_port       = "8081"
  trust_domain      = "${TRUST_DOMAIN}"
  socket_path       = "/tmp/spire-agent/public/api.sock"
  trust_bundle_path = "/etc/spire/bootstrap.crt"
}
plugins {
  NodeAttestor     "join_token" {}
  KeyManager       "memory"     {}
  WorkloadAttestor "unix"       {}
}
EOF

cat > /etc/systemd/system/spire-agent.service <<EOF
[Unit]
Description=SPIRE Agent
After=network.target spire-server.service

[Service]
ExecStartPre=/bin/mkdir -p /tmp/spire-agent/public
ExecStart=/usr/local/bin/spire-agent run \
  -config /etc/spire/agent.conf \
  -joinToken ${JOIN_TOKEN}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=spire-agent

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now spire-agent

# ── wait for agent ────────────────────────────
echo "==> Waiting for SPIRE agent to be ready..."
for i in {1..15}; do
  if spire-agent healthcheck -socketPath /tmp/spire-agent/public/api.sock &>/dev/null; then
    echo "    Agent is healthy (attempt ${i})"
    break
  fi
  if [ "$i" -eq 15 ]; then
    echo "ERROR: Agent failed to start. Check logs:"
    echo "       journalctl -u spire-agent --no-pager -n 50"
    exit 1
  fi
  echo "    ... waiting (${i}/15)"
  sleep 2
done

# ─────────────────────────────────────────────
# Done
# ─────────────────────────────────────────────
echo ""
echo "✓ SPIRE setup complete"
echo ""
echo "  Trust domain : ${TRUST_DOMAIN}"
echo "  Server socket: /run/spire/sockets/api.sock"
echo "  Agent socket : /tmp/spire-agent/public/api.sock"
echo ""
echo "  Registered entries:"
spire-server entry show -socketPath /tmp/spire-server/private/api.sock
echo ""
echo "  Useful commands:"
echo "    journalctl -u spire-server -f"
echo "    journalctl -u spire-agent -f"
echo "    sudo spire-server healthcheck"
echo "    spire-agent healthcheck"
