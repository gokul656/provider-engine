#!/bin/bash
set -euo pipefail

# Self-escalate if not root
if [[ $EUID -ne 0 ]]; then
  exec sudo bash "$0" "$@"
fi

sudo apt install -y python3 python3-pip python3-venv

python3 -m venv /opt/dcp-cp
source /opt/dcp-cp/bin/activate

pip install \
  fastapi "uvicorn[standard]" \
  redis asyncpg \
  cryptography "python-jose[cryptography]" \
  spiffe paramiko \
  geoip2 httpx pydantic

# redis — session store + port registry
sudo apt install -y redis-server
sudo sed -i 's/^bind .*/bind 127.0.0.1/' /etc/redis/redis.conf

REDIS_PASS=$(openssl rand -hex 32)
echo "requirepass $REDIS_PASS" | sudo tee -a /etc/redis/redis.conf
echo "Redis password: $REDIS_PASS"  # save this

sudo systemctl enable --now redis-server
redis-cli -a $REDIS_PASS ping

# postgresql — provider registry
sudo apt install -y postgresql postgresql-contrib
sudo systemctl enable --now postgresql

DB_PASS=$(openssl rand -hex 32)
sudo -u postgres psql << EOF
CREATE USER dcp WITH PASSWORD '$DB_PASS';
CREATE DATABASE dcp_registry OWNER dcp;
EOF

echo "PostgreSQL password: $DB_PASS"  # save this
pg_isready

# ssh certificate authority
sudo mkdir -p /etc/dcp/ssh-ca
sudo chmod 700 /etc/dcp/ssh-ca

sudo ssh-keygen \
  -t ecdsa -b 521 \
  -f /etc/dcp/ssh-ca/platform_ca \
  -N "" \
  -C "dcp-platform-ssh-ca-$(date +%Y%m%d)"

sudo chmod 600 /etc/dcp/ssh-ca/platform_ca
sudo chmod 644 /etc/dcp/ssh-ca/platform_ca.pub

echo "=== bake into every VM sshd at provisioning time ==="
cat /etc/dcp/ssh-ca/platform_ca.pub

# The CA private key at /etc/dcp/ssh-ca/platform_ca is the most sensitive secret in the system. Move it to HashiCorp Vault or AWS KMS in production. open firewall ports
sudo ufw allow 443/tcp    # HTTPS API
sudo ufw allow 8081/tcp   # SPIRE server
sudo ufw allow 51820/udp  # WireGuard relay
sudo ufw reload
