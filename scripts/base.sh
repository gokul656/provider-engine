#!/bin/bash
set -euo pipefail

# Self-escalate if not root
if [[ $EUID -ne 0 ]]; then
  exec sudo bash "$0" "$@"
fi

sudo apt install -y \
    build-essential \
    curl \
    git \
    htop \
    neovim \
    python3-pip \
    software-properties-common \
    unzip \
    wget \
    jq \
    fail2ban \
    chrony \
    ufw \
    net-tools \
    ca-certificates \
    apt-transport-https \
    gnupg \
    lsb-release \
    openssh-server \
    figlet

# Setup SSH server
sudo systemctl enable --now ssh

# NTP ( clock skew kills SVID validation ) 
sudo systemctl enable chrony
sudo systemctl start chrony

# Harden SSH - disable pass auth

sudo sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config
sudo sed -i 's/#PermitRootLogin yes/PermitRootLogin no/' /etc/ssh/sshd_config
sudo systemctl restart sshd

# Basic firewall setup - allow SSH only on start

sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow ssh
sudo ufw enable

