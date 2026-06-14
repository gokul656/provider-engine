#!/bin/bash
set -euo pipefail

# Self-escalate if not root
if [[ $EUID -ne 0 ]]; then
  exec sudo bash "$0" "$@"
fi

sudo apt install -y cpu-checker
kvm-ok  # Must return "KVM acceleration can be used"
sudo modprobe kvm

# Setup Firecracker & Jailer

ARCH="$(uname -m)"
release_url="https://github.com/firecracker-microvm/firecracker/releases"
latest=$(basename $(curl -fsSLI -o /dev/null -w  %{url_effective} ${release_url}/latest))
curl -L ${release_url}/download/${latest}/firecracker-${latest}-${ARCH}.tgz | tar -xz

# Move both binaries into place
cd release-${latest}-${ARCH}
sudo cp firecracker-${latest}-${ARCH} /usr/local/bin/firecracker
sudo cp jailer-${latest}-${ARCH} /usr/local/bin/jailer
sudo chmod +x /usr/local/bin/firecracker /usr/local/bin/jailer

cd ..
echo "Release ${latest} installed successfully."
echo "Cleaning up... removing release-${latest}-${ARCH} directory."
rm -rf release-${latest}-${ARCH}

# Verify
firecracker --version
jailer --version

# Deps for building microVMs
sudo apt install -y \
    libseccomp-dev \
    libseccomp2 \
    seccomp \
    cryptsetup \
    iproute2 \
    iptables \
    qemu-utils

sudo mkdir -p \
    /opt/dcp/kernels \
    /var/lib/dcp/vms \
    /var/lib/dcp/rootfs \
    /var/lib/dcp/sockets

ARCH="$(uname -m)"

sudo chown -R $USER:$USER /opt/dcp
curl -Lo /opt/dcp/kernels/vmlinux.bin "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.10/${ARCH}/vmlinux-5.10.225"
