#!/bin/bash
set -euo pipefail

# Self-escalate if not root
if [[ $EUID -ne 0 ]]; then
  exec sudo bash "$0" "$@"
fi

sudo apt install -y wireguard wireguard-tools

# Setup WireGuard Keypair
wg genkey | sudo tee /etc/wireguard/cp_private | wg pubkey | sudo tee /etc/wireguard/cp_public
wg genkey | sudo tee /etc/wireguard/prov_private | wg pubkey | sudo tee /etc/wireguard/prov_public


sudo chmod 600 /etc/wireguard/cp_private /etc/wireguard/prov_private
# cat /etc/wireguard/cp_public # to be shared with control plane

# Setup Control Plane WireGuard config

sudo tee /etc/wireguard/wg0.conf << EOF
[Interface]
Address = 10.99.0.1/24
ListenPort = 51820
PrivateKey = $(sudo cat /etc/wireguard/prov_private)

[Peer]
PublicKey = $(sudo cat /etc/wireguard/cp_public)
AllowedIPs = 10.99.0.2/32
EOF

sudo chmod 600 /etc/wireguard/wg0.conf
sudo wg-quick up wg0


# Setup Provider WireGuard config

sudo tee /etc/wireguard/wg1.conf << EOF
[Interface]
Address = 10.99.0.2/24
ListenPort = 51821
PrivateKey = $(sudo cat /etc/wireguard/prov_private)

[Peer]
PublicKey = $(sudo cat /etc/wireguard/cp_public)
Endpoint = 127.0.0.1:51820
AllowedIPs = 10.99.0.1/32
PersistentKeepalive = 25
EOF

sudo chmod 600 /etc/wireguard/wg1.conf
sudo wg-quick up wg1

# Verify connectivity

ping -c 3 10.99.0.1
ping -c 3 10.99.0.2

sudo wg show
