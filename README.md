Decentralized Compute Platform — Ubuntu Setup Guide
Security-first setup for a decentralized compute platform with WireGuard mesh networking, Firecracker microVMs, and SPIFFE/SPIRE workload identity.
🟦 control-plane — run on your server only
 🟩 provider — run on buyer machines
 🟪 both — run on all machines

Table of Contents
Prerequisites & Assumptions
Base System — Both
WireGuard — Both
Firecracker microVMs — Provider
SPIRE Server — Control Plane
SPIRE Agent — Both
Control Plane Services — Control Plane
SSH Certificate Authority — Control Plane
Provider Agent — Provider
Firewall Rules Summary
Boot Order & Health Checks
What You Do NOT Need

1. Prerequisites & Assumptions
Item
Requirement
OS
Ubuntu 22.04 LTS or 24.04 LTS
Control plane
1 VPS with a static IP (e.g. Hetzner CX21, $6/mo)
Provider machines
Any Ubuntu machine — no static IP needed
KVM
Required on provider machines (kvm-ok must pass)
Kernel
5.6+ (WireGuard built-in). Ubuntu 22.04 ships with 5.15 ✓
# Run this first on any machine to check readiness
uname -r                  # should be 5.6+
sudo apt install -y cpu-checker && kvm-ok   # provider machines only

⚠️ KVM check is critical. If kvm-ok fails on a provider machine, Firecracker will not work. The buyer must enable virtualisation in BIOS before installing.

2. Base System — both
Run on every machine (control plane and all provider machines) before anything else.
sudo apt update && sudo apt upgrade -y

# Core tools
```
sudo apt install -y \
  curl wget git vim jq unzip \
  build-essential \
  apt-transport-https \
  ca-certificates \
  gnupg \
  lsb-release \
  ufw \
  fail2ban
```

# NTP — CRITICAL. Short-lived SPIFFE certs will be rejected if clock drifts.
```
sudo apt install -y chrony
sudo systemctl enable chrony
sudo systemctl start chrony
```

# Verify clock is synced
```
chronyc tracking | grep "System time"
```

# Harden SSH
```
sudo sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config
sudo sed -i 's/#PermitRootLogin yes/PermitRootLogin no/'             /etc/ssh/sshd_config
sudo sed -i 's/#MaxAuthTries 6/MaxAuthTries 3/'                      /etc/ssh/sshd_config
sudo systemctl reload sshd

# Base firewall — allow SSH, deny everything else inbound
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp
sudo ufw enable

```

3. WireGuard — both
WireGuard is built into the Linux kernel since 5.6. Only the tools package is needed.
3.1 Install

```
sudo apt install -y wireguard wireguard-tools
```
3.2 Generate keypair — run separately on EACH machine
# Generate keys
```
wg genkey | sudo tee /etc/wireguard/privatekey | wg pubkey | sudo tee /etc/wireguard/publickey

sudo chmod 600 /etc/wireguard/privatekey
cat /etc/wireguard/publickey   # register this with the control plane

3.3 Control plane — relay interface
# Enable IP forwarding (relay must forward packets between peers)
echo "net.ipv4.ip_forward=1"   | sudo tee -a /etc/sysctl.conf
echo "net.ipv6.conf.all.forwarding=1" | sudo tee -a /etc/sysctl.conf
sudo sysctl -p

# Create WireGuard relay config
sudo tee /etc/wireguard/wg0.conf << EOF
[Interface]
Address    = 10.99.0.1/24
ListenPort = 51820
PrivateKey = $(sudo cat /etc/wireguard/privatekey)

# Providers and users are added here dynamically by the control plane API
# when they register or request an SSH session.
EOF

sudo chmod 600 /etc/wireguard/wg0.conf

# Start relay
sudo systemctl enable wg-quick@wg0
sudo systemctl start wg-quick@wg0

# Open WireGuard port
sudo ufw allow 51820/udp
```

3.4 Provider machines — persistent peer
On provider machines, the wg0.conf is generated automatically by the provider agent at startup using config returned by the control plane. You do not manually configure it. The key thing is the PersistentKeepalive — this keeps the NAT mapping alive in the provider's home router.
# This is what the provider agent writes at runtime — shown here for reference
sudo tee /etc/wireguard/wg0.conf << EOF
[Interface]
Address    = 10.99.0.X/24          # assigned by control plane
PrivateKey = $(sudo cat /etc/wireguard/privatekey)

[Peer]
PublicKey           = <relay-pubkey>
Endpoint            = relay.dcp.io:51820
AllowedIPs          = 10.99.0.0/24
PersistentKeepalive = 25           # critical — keeps NAT open, refreshes endpoint if IP changes
EOF

sudo systemctl enable wg-quick@wg0
sudo systemctl start wg-quick@wg0


4. Firecracker microVMs — provider
Firecracker runs each user workload in a hardware-isolated KVM microVM. No shared kernel with the host.
4.1 KVM access
sudo usermod -aG kvm $USER
# Log out and back in for group to take effect

4.2 Install Firecracker + Jailer
# Get latest version tag
FIRECRACKER_VERSION=$(curl -s \
  https://api.github.com/repos/firecracker-microvm/firecracker/releases/latest \
  | jq -r .tag_name)

# Download
curl -Lo /tmp/firecracker.tgz \
  "https://github.com/firecracker-microvm/firecracker/releases/download/${FIRECRACKER_VERSION}/firecracker-${FIRECRACKER_VERSION}-x86_64.tgz"

tar -xzf /tmp/firecracker.tgz -C /tmp

# Install binaries
sudo mv /tmp/release-${FIRECRACKER_VERSION}-x86_64/firecracker-${FIRECRACKER_VERSION}-x86_64 \
  /usr/local/bin/firecracker
sudo mv /tmp/release-${FIRECRACKER_VERSION}-x86_64/jailer-${FIRECRACKER_VERSION}-x86_64 \
  /usr/local/bin/jailer

sudo chmod +x /usr/local/bin/firecracker /usr/local/bin/jailer

# Verify
firecracker --version
jailer --version

4.3 Kernel and dependencies
# Seccomp (syscall filtering on the host process)
sudo apt install -y libseccomp-dev libseccomp2 seccomp

# LUKS disk encryption (VM disks encrypted, key held by control plane)
sudo apt install -y cryptsetup

# Network tools (for per-VM tap interfaces and namespaces)
sudo apt install -y iproute2 iptables

# Download a Firecracker-compatible stripped kernel
# (Firecracker cannot use the host kernel — needs its own minimal one)
sudo mkdir -p /opt/dcp/kernels
curl -Lo /opt/dcp/kernels/vmlinux.bin \
  https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/x86_64/kernels/vmlinux.bin

# Directory structure for VM state
sudo mkdir -p /var/lib/dcp/vms
sudo mkdir -p /var/lib/dcp/rootfs
sudo mkdir -p /var/lib/dcp/sockets

⚠️ Firecracker requires bare-metal KVM. It will NOT work inside a VM unless the cloud provider explicitly supports nested virtualisation (AWS .metal instances, GCP with --enable-nested-virtualization).

5. SPIRE Server — control-plane
SPIRE server is the certificate authority that issues short-lived SPIFFE identities to all services.
5.1 Install
SPIRE_VERSION="1.9.4"

curl -Lo /tmp/spire.tar.gz \
  "https://github.com/spiffe/spire/releases/download/v${SPIRE_VERSION}/spire-${SPIRE_VERSION}-linux-amd64-musl.tar.gz"

tar -xzf /tmp/spire.tar.gz -C /tmp
sudo mv /tmp/spire-${SPIRE_VERSION} /opt/spire

sudo ln -s /opt/spire/bin/spire-server /usr/local/bin/spire-server
sudo ln -s /opt/spire/bin/spire-agent  /usr/local/bin/spire-agent

5.2 Configure SPIRE server
sudo mkdir -p /etc/spire /var/lib/spire/server

sudo tee /etc/spire/server.conf << 'EOF'
server {
  bind_address = "0.0.0.0"
  bind_port    = "8081"
  trust_domain = "dcp.io"
  data_dir     = "/var/lib/spire/server"
  log_level    = "INFO"

  # SVIDs expire every hour — limits blast radius if a cert is stolen
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

  # join_token is fine for development.
  # Replace with tpm_devid or aws_iid for production hardware attestation.
  NodeAttestor "join_token" {}
}
EOF

5.3 Run as systemd service
sudo tee /etc/systemd/system/spire-server.service << 'EOF'
[Unit]
Description=SPIRE Server
After=network.target

[Service]
ExecStart=/usr/local/bin/spire-server run -config /etc/spire/server.conf
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable spire-server
sudo systemctl start spire-server

# Verify
spire-server healthcheck

5.4 Register internal service identities
# Generate a join token for the control plane node's agent
JOIN_TOKEN=$(spire-server token generate -spiffeID spiffe://dcp.io/node/control-plane | awk '{print $2}')
echo "Join token: $JOIN_TOKEN"   # save this — needed for agent config

# Register each control plane service
# Each gets its own SPIFFE ID and is only callable by specific other services

spire-server entry create \
  -spiffeID spiffe://dcp.io/api-gateway \
  -parentID spiffe://dcp.io/node/control-plane \
  -selector unix:user:api-gateway

spire-server entry create \
  -spiffeID spiffe://dcp.io/auth-service \
  -parentID spiffe://dcp.io/node/control-plane \
  -selector unix:user:auth-svc

spire-server entry create \
  -spiffeID spiffe://dcp.io/registry \
  -parentID spiffe://dcp.io/node/control-plane \
  -selector unix:user:registry-svc

spire-server entry create \
  -spiffeID spiffe://dcp.io/cert-issuer \
  -parentID spiffe://dcp.io/node/control-plane \
  -selector unix:user:cert-issuer-svc

# Open SPIRE server port (agents connect to this)
sudo ufw allow 8081/tcp


6. SPIRE Agent — both
The SPIRE agent runs on every node (control plane and providers). It fetches SVIDs from the server and serves them to local processes via a Unix socket.
sudo mkdir -p /etc/spire /var/lib/spire/agent /tmp/spire-agent/public

sudo tee /etc/spire/agent.conf << EOF
agent {
  data_dir       = "/var/lib/spire/agent"
  log_level      = "INFO"
  server_address = "YOUR_CONTROL_PLANE_IP"   # replace with actual IP
  server_port    = "8081"
  trust_domain   = "dcp.io"
  socket_path    = "/tmp/spire-agent/public/api.sock"
}

plugins {
  NodeAttestor "join_token" {}
  KeyManager   "memory"     {}
  WorkloadAttestor "unix"   {}
}
EOF

# Systemd service
sudo tee /etc/systemd/system/spire-agent.service << EOF
[Unit]
Description=SPIRE Agent
After=network.target

[Service]
# Replace YOUR_JOIN_TOKEN with the token generated in step 5.4
ExecStart=/usr/local/bin/spire-agent run \
  -config /etc/spire/agent.conf \
  -joinToken YOUR_JOIN_TOKEN
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable spire-agent
sudo systemctl start spire-agent

# Verify — should return "Agent is healthy"
spire-agent healthcheck -socketPath /tmp/spire-agent/public/api.sock


7. Control Plane Services — control-plane
7.1 Python runtime
sudo apt install -y python3 python3-pip python3-venv

python3 -m venv /opt/dcp-cp
source /opt/dcp-cp/bin/activate

pip install \
  fastapi \
  "uvicorn[standard]" \
  redis \
  asyncpg \
  cryptography \
  "python-jose[cryptography]" \
  spiffe \
  paramiko \
  geoip2 \
  httpx \
  pydantic \
  python-multipart

7.2 Redis (session store, port registry)
sudo apt install -y redis-server

# Bind to localhost only — never expose Redis publicly
sudo sed -i 's/^bind .*/bind 127.0.0.1/' /etc/redis/redis.conf

# Require a password
REDIS_PASS=$(openssl rand -hex 32)
echo "requirepass $REDIS_PASS" | sudo tee -a /etc/redis/redis.conf
echo "Redis password: $REDIS_PASS"   # save this

sudo systemctl enable redis-server
sudo systemctl restart redis-server
redis-cli -a $REDIS_PASS ping   # should return PONG

7.3 PostgreSQL (provider registry, persistent state)
sudo apt install -y postgresql postgresql-contrib
sudo systemctl enable postgresql
sudo systemctl start postgresql

# Create database and user
DB_PASS=$(openssl rand -hex 32)
sudo -u postgres psql << EOF
CREATE USER dcp WITH PASSWORD '$DB_PASS';
CREATE DATABASE dcp_registry OWNER dcp;
\q
EOF

echo "PostgreSQL password: $DB_PASS"   # save this

# Verify
pg_isready

7.4 Open API port
sudo ufw allow 443/tcp   # HTTPS API (put Nginx or Caddy in front of uvicorn)

# Optional: install Caddy as reverse proxy with automatic TLS
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update && sudo apt install -y caddy


8. SSH Certificate Authority — control-plane
This is the most sensitive component. The CA key signs all user SSH certificates.
sudo mkdir -p /etc/dcp/ssh-ca
sudo chmod 700 /etc/dcp/ssh-ca

# Generate the platform SSH CA keypair
sudo ssh-keygen \
  -t ecdsa -b 521 \
  -f /etc/dcp/ssh-ca/platform_ca \
  -N "" \
  -C "dcp-platform-ssh-ca-$(date +%Y%m%d)"

sudo chmod 600 /etc/dcp/ssh-ca/platform_ca
sudo chmod 644 /etc/dcp/ssh-ca/platform_ca.pub

echo "=== CA public key (bake this into every VM's sshd_config at provisioning time) ==="
cat /etc/dcp/ssh-ca/platform_ca.pub

This public key gets written into every Firecracker VM's /etc/ssh/sshd_config at provisioning time:
# In each VM's sshd_config:
TrustedUserCAKeys /etc/ssh/dcp_ca.pub
AuthorizedPrincipalsFile /etc/ssh/authorized_principals/%u

⚠️ In production, do not store the CA private key on disk. Move it to HashiCorp Vault or AWS KMS. The cert-issuer service should call the KMS signing API rather than reading a file. A compromised CA key lets anyone issue valid SSH certificates to any VM indefinitely.

9. Provider Agent — provider
This is the daemon that runs on buyer machines after they click "Start". It handles registration, WireGuard config, VM provisioning, and LUKS key fetching — all automatically.
# Additional dependencies
sudo apt install -y \
  autossh \
  qemu-utils \
  python3 python3-pip

pip3 install requests psutil cryptography

# Create directories
sudo mkdir -p /opt/dcp-provider
sudo mkdir -p /var/lib/dcp/vms
sudo mkdir -p /var/lib/dcp/rootfs
sudo mkdir -p /var/log/dcp

# Provider daemon systemd service
sudo tee /etc/systemd/system/dcp-provider.service << 'EOF'
[Unit]
Description=DCP Provider Agent
After=network.target spire-agent.service wg-quick@wg0.service

[Service]
EnvironmentFile=/etc/dcp/provider.env
ExecStart=/usr/local/bin/dcp-provider \
  --control-plane https://cp.dcp.io \
  --token ${PROVIDER_TOKEN}
Restart=always
RestartSec=10
StandardOutput=append:/var/log/dcp/provider.log
StandardError=append:/var/log/dcp/provider.log

[Install]
WantedBy=multi-user.target
EOF

# Environment file — provider token written here at install time
sudo tee /etc/dcp/provider.env << 'EOF'
PROVIDER_TOKEN=REPLACE_WITH_TOKEN_FROM_DASHBOARD
EOF
sudo chmod 600 /etc/dcp/provider.env

sudo systemctl daemon-reload
sudo systemctl enable dcp-provider


10. Firewall Rules Summary
Control plane server
sudo ufw default deny incoming
sudo ufw default allow outgoing

sudo ufw allow 22/tcp      # SSH (management only — lock down to your IP in prod)
sudo ufw allow 443/tcp     # HTTPS API
sudo ufw allow 8081/tcp    # SPIRE server (agents connect here)
sudo ufw allow 51820/udp   # WireGuard relay

# Do NOT open port 22 for end users — SSH is routed through WireGuard
sudo ufw reload
sudo ufw status verbose

Provider machines
sudo ufw default deny incoming
sudo ufw default allow outgoing

sudo ufw allow 22/tcp      # SSH (management — lock to your IP)
sudo ufw allow 51820/udp   # WireGuard (outbound handshake, but allow inbound reply)

# No other inbound ports needed — everything else is outbound-initiated
sudo ufw reload


11. Boot Order & Health Checks
Start services in this exact order. Each layer depends on the one before it.
Control plane:
  1. chrony          (clock sync)
  2. postgresql      (data layer)
  3. redis-server    (session store)
  4. spire-server    (identity CA — must be up before any agents)
  5. wg-quick@wg0   (WireGuard relay)
  6. spire-agent     (fetches SVIDs for local services)
  7. FastAPI services (auth, registry, cert-issuer)
  8. caddy           (TLS termination)

Provider machine:
  1. chrony
  2. spire-agent     (fetches provider identity from CP)
  3. wg-quick@wg0   (connects to relay)
  4. dcp-provider    (registers, provisions VMs)

Health check script
Save as /usr/local/bin/dcp-health and run after startup:
#!/bin/bash
set -e

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo " DCP Platform Health Check"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

echo ""
echo "[ NTP / Clock ]"
chronyc tracking | grep -E "System time|Stratum"

echo ""
echo "[ WireGuard ]"
sudo wg show wg0 | grep -E "interface|peer|endpoint|latest handshake"

echo ""
echo "[ SPIRE Server ]"
spire-server healthcheck 2>&1 || echo "SPIRE server not running or not on this node"

echo ""
echo "[ SPIRE Agent ]"
spire-agent healthcheck \
  -socketPath /tmp/spire-agent/public/api.sock 2>&1

echo ""
echo "[ Redis ]"
redis-cli ping 2>&1

echo ""
echo "[ PostgreSQL ]"
pg_isready 2>&1

echo ""
echo "[ Firecracker ]"
firecracker --version 2>&1

echo ""
echo "[ KVM ]"
ls /dev/kvm && echo "KVM available" || echo "KVM NOT available"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

sudo chmod +x /usr/local/bin/dcp-health
dcp-health


12. What You Do NOT Need
Things you might expect to install but are deliberately excluded:
Tool
Why excluded
Docker / containerd
Containers share the host kernel — replaced by Firecracker microVMs
OpenVPN
Replaced by WireGuard — simpler, faster, smaller attack surface
autossh (primary tunnel)
Replaced by WireGuard — autossh only as fallback if WireGuard fails
Static IP on provider
WireGuard roaming handles dynamic IPs transparently
Tailscale / Netbird
You're running your own WireGuard coordination — no managed service needed
HashiCorp Consul
SPIRE handles service identity; Redis handles registration state
Kubernetes
Overkill for v1; Firecracker + systemd is simpler and more secure for this use case

Generated for Ubuntu 22.04 LTS / 24.04 LTS · Last updated June 2026
