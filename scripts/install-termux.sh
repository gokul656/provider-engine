#!/data/data/com.termux/files/usr/bin/bash
# install-termux.sh — DCP provider install for Android Termux (no root required)
# Usage: bash install-termux.sh
#
# After this runs, start the provider with:
#   dcp-provider --mode ssh --cp-url http://<CP_IP>:8080 --token <TOKEN>

set -euo pipefail

echo ""
echo "DCP Provider — Termux install"
echo "────────────────────────────────────────"

# 1. Packages
echo "==> Installing packages..."
pkg update -y -q
pkg install -y -q \
  openssh \
  wireguard-go \
  iproute2

echo "    ✓ openssh, wireguard-go, iproute2"

# 2. Start sshd (Termux sshd runs on port 8022, no root needed)
echo "==> Starting sshd..."
if ! pgrep -x sshd > /dev/null; then
  sshd
fi
echo "    ✓ sshd running on port 8022"

# 3. Place dcp-provider binary
echo "==> Installing dcp-provider binary..."
BINARY_DIR="$HOME/.local/bin"
mkdir -p "$BINARY_DIR"

if [[ ! -f "$BINARY_DIR/dcp-provider" ]]; then
  echo "    Place the dcp-provider binary (linux/arm64) at:"
  echo "    $BINARY_DIR/dcp-provider"
  echo ""
  echo "    Build it on your dev machine:"
  echo "      cd dcp-provider"
  echo "      GOOS=linux GOARCH=arm64 go build -o dcp-provider-android ."
  echo "    Then copy via adb or scp."
else
  chmod +x "$BINARY_DIR/dcp-provider"
  echo "    ✓ dcp-provider found at $BINARY_DIR/dcp-provider"
fi

# 4. Add to PATH
SHELL_RC="$HOME/.bashrc"
if [[ -f "$HOME/.zshrc" ]]; then SHELL_RC="$HOME/.zshrc"; fi
if ! grep -q "$BINARY_DIR" "$SHELL_RC" 2>/dev/null; then
  echo "export PATH=\"$BINARY_DIR:\$PATH\"" >> "$SHELL_RC"
fi

# 5. Termux boot (auto-start on device reboot via Termux:Boot app)
BOOT_DIR="$HOME/.termux/boot"
mkdir -p "$BOOT_DIR"
cat > "$BOOT_DIR/dcp-provider.sh" << 'EOF'
#!/data/data/com.termux/files/usr/bin/bash
# Auto-started by Termux:Boot on device reboot
source $HOME/.dcp/provider.env 2>/dev/null || true
sshd
$HOME/.local/bin/dcp-provider \
  --mode ssh \
  --cp-url "$DCP_CP_URL" \
  --token  "$DCP_TOKEN" \
  --location "${DCP_LOCATION:-android}" \
  >> $HOME/.dcp/provider.log 2>&1 &
EOF
chmod +x "$BOOT_DIR/dcp-provider.sh"
echo "    ✓ Termux:Boot script written → $BOOT_DIR/dcp-provider.sh"

# 6. Env file template
mkdir -p "$HOME/.dcp"
ENV_FILE="$HOME/.dcp/provider.env"
if [[ ! -f "$ENV_FILE" ]]; then
  cat > "$ENV_FILE" << 'EOF'
DCP_CP_URL=http://YOUR_CP_IP:8080
DCP_TOKEN=YOUR_REGISTRATION_TOKEN
DCP_LOCATION=android
EOF
  echo "    ✓ Env template written → $ENV_FILE"
fi

echo ""
echo "────────────────────────────────────────"
echo " Done. Next steps:"
echo ""
echo " 1. Edit your env file:"
echo "      nano $ENV_FILE"
echo ""
echo " 2. Source it and run:"
echo "      source $ENV_FILE"
echo "      dcp-provider --mode ssh --cp-url \$DCP_CP_URL --token \$DCP_TOKEN"
echo ""
echo " 3. For auto-start on reboot, install the Termux:Boot app"
echo "    from F-Droid and enable it."
echo ""
echo " Note: WireGuard keys are stored in ~/.dcp/wireguard/"
echo "       SSH keys are in ~/.ssh/authorized_keys"
echo "────────────────────────────────────────"
