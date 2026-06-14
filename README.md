# DCP — Decentralized Compute Platform

Two Go binaries that turn any Ubuntu machine into a compute provider or control plane for a decentralized cloud.

| Binary | Role |
|--------|------|
| `dcp-cp` | Control plane — provider registry, job dispatch, SSH CA, WireGuard relay |
| `dcp-provider` | Provider agent — registers with CP, boots Firecracker microVMs (or Docker fallback) |

---

## Prerequisites

| Requirement | Control Plane | Provider |
|-------------|:---:|:---:|
| Ubuntu 22.04 / 24.04 LTS | ✓ | ✓ |
| Static public IP | ✓ | — |
| KVM (`kvm-ok` passes) | — | ✓ |
| Linux kernel 5.6+ (WireGuard built-in) | ✓ | ✓ |
| Root / sudo access | ✓ | ✓ |
| Go 1.21+ (build machine only) | ✓ | ✓ |

> **KVM is required on provider machines.** If `kvm-ok` fails, the provider falls back to Docker containers (no hardware isolation). Enable VT-x / AMD-V in BIOS.

---

## Architecture

```
          ┌─────────────────────────────────┐
          │        Control Plane            │
          │  dcp-cp serve                   │
          │  ┌──────────┐  ┌─────────────┐ │
          │  │ Registry │  │ Scheduler   │ │
          │  │ Postgres │  │ Redis queue │ │
          │  └──────────┘  └─────────────┘ │
          │  ┌──────────┐  ┌─────────────┐ │
          │  │ SSH CA   │  │ WireGuard   │ │
          │  │ ECDSA    │  │ wg0 relay   │ │
          │  └──────────┘  └─────────────┘ │
          └────────────────┬────────────────┘
                           │ WireGuard (10.99.0.0/16)
          ┌────────────────┴────────────────┐
          │                                 │
   ┌──────┴──────┐                  ┌───────┴──────┐
   │  Provider A │                  │  Provider B  │
   │ dcp-provider│                  │ dcp-provider │
   │ Firecracker │                  │  Docker      │
   │  microVMs   │                  │  (no KVM)    │
   └─────────────┘                  └──────────────┘
```

---

## Quick Start

### Step 1 — Install packages on each machine

```bash
# Control plane machine
sudo bash scripts/install.sh cp

# Each provider machine
sudo bash scripts/install.sh provider

# Single machine (dev / single-node)
sudo bash scripts/install.sh both
```

`install.sh` only installs apt packages and downloads third-party binaries
(Firecracker, SPIRE). It does **not** configure anything — that is done by the Go CLIs.

### Step 2 — Build the binaries

```bash
# Requires Go 1.21+ on your build machine
make build

# Cross-compile for Linux deploy targets
make release
# → dcp-control-plane/dist/dcp-cp-linux-amd64
# → dcp-provider/dist/dcp-provider-linux-amd64
```

### Step 3 — Copy binaries to target machines

```bash
sudo cp dcp-control-plane/dcp-cp    /usr/local/bin/dcp-cp
sudo cp dcp-provider/dcp-provider   /usr/local/bin/dcp-provider

# Or deploy remotely in one step
make deploy-cp       HOST=root@<CP_IP>
make deploy-provider HOST=root@<PROVIDER_IP>
```

---

## Control Plane Setup

Run once on the control plane machine:

```bash
sudo dcp-cp setup
```

This configures (idempotent — safe to re-run):

| What | Where |
|------|-------|
| ECDSA SSH CA key | `/etc/dcp/ssh-ca/platform_ca` |
| WireGuard keypair + `wg0` interface up | `/etc/wireguard/cp_private`, `cp_public` |
| SPIRE server config + systemd unit | `/etc/spire/server.conf` |
| SPIRE join token (for agents) | `/etc/spire/join_token` |
| SPIRE agent config + systemd unit | `/etc/spire/agent.conf` |
| PostgreSQL user + database | `dcp_registry` · password in `/etc/dcp/pg_pass` |
| JWT signing secret | `/etc/dcp/jwt_secret` |
| `dcp-cp` systemd service unit | `/etc/systemd/system/dcp-cp.service` |
| UFW rules | 22, 443, 8080, 8081/tcp · 51820/udp |

Then start the server:

```bash
sudo systemctl enable --now dcp-cp
```

The WireGuard endpoint advertised to providers is a runtime flag (not setup):

```bash
# Pass directly
dcp-cp serve --wg-endpoint 1.2.3.4:51820 --jwt-secret $(cat /etc/dcp/jwt_secret)

# Or via environment variable (recommended for systemd)
sudo systemctl edit dcp-cp
# Add: Environment=DCP_WG_ENDPOINT=1.2.3.4:51820
```

### Generate provider tokens

```bash
# DCP registration token + SPIRE join token (needed for full provider setup)
dcp-cp token generate --spire

# Output:
# DCP_TOKEN=abc123...
# SPIRE_JOIN_TOKEN=xyz789...

# Generate multiple pairs at once
dcp-cp token generate --spire -n 5
```

---

## Provider Setup

On each provider machine, run once with the tokens from the CP:

```bash
sudo dcp-provider setup \
  --cp-url      http://<CP_IP>:8080 \
  --token       <DCP_TOKEN> \
  --spire-token <SPIRE_JOIN_TOKEN> \
  --cp-ip       <CP_IP> \
  --location    us-east
```

This configures:

| What | Where |
|------|-------|
| WireGuard keypair | `/etc/wireguard/prov_private`, `prov_public` |
| SPIRE agent config + systemd unit | `/etc/spire/agent.conf` |
| SPIRE join token | `/etc/spire/join_token` |
| Provider env file | `/etc/dcp/provider.env` |
| `dcp-provider` systemd service unit | `/etc/systemd/system/dcp-provider.service` |
| UFW rules | 22/tcp · 51821/udp |

Then start the agent:

```bash
sudo systemctl enable --now dcp-provider
```

On first boot the provider agent will:
1. Register with the CP (sends WireGuard pubkey + resources)
2. Receive a `wg0.conf` from the CP and bring up the WireGuard interface
3. Start heartbeating every 30 seconds
4. Long-poll for deploy jobs and boot Firecracker microVMs (or Docker if no KVM)

---

## Single-Machine Setup (Dev / Testing)

Run CP and provider on one Ubuntu machine with WireGuard loopback:

```bash
# 1. Install packages
sudo bash scripts/install.sh both

# 2. Build and install
make build
sudo cp dcp-control-plane/dcp-cp    /usr/local/bin/dcp-cp
sudo cp dcp-provider/dcp-provider   /usr/local/bin/dcp-provider

# 3. Configure and start CP
sudo dcp-cp setup
sudo systemctl enable --now dcp-cp

# 4. Generate tokens
dcp-cp token generate --spire
# → copy DCP_TOKEN and SPIRE_JOIN_TOKEN

# 5. Configure and start provider
sudo dcp-provider setup \
  --cp-url      http://127.0.0.1:8080 \
  --token       <DCP_TOKEN> \
  --spire-token <SPIRE_JOIN_TOKEN> \
  --cp-ip       127.0.0.1 \
  --location    local

sudo systemctl enable --now dcp-provider

# 6. Verify everything
make health
```

---

## Boot Order

Systemd `After=` handles ordering automatically after setup. For manual start:

```
Control plane:
  1. chrony          — clock sync (SPIRE rejects drifted clocks)
  2. postgresql
  3. redis-server
  4. spire-server    — identity CA, must be up before agents
  5. wg-quick@wg0   — WireGuard relay
  6. spire-agent     — fetches SVIDs for local services
  7. dcp-cp

Provider:
  1. chrony
  2. spire-agent     — connects to CP SPIRE server on port 8081
  3. dcp-provider    — registers, brings up wg0, polls for jobs
```

---

## Health Check

```bash
make health
# or
sudo bash scripts/health.sh
```

Checks: chrony · WireGuard · SPIRE server · SPIRE agent · Redis · PostgreSQL · Firecracker · KVM · dcp-cp · dcp-provider

---

## CLI Reference

### `dcp-cp`

```
dcp-cp setup
  Configure this machine as CP — SSH CA, WireGuard, SPIRE, Postgres, systemd units

dcp-cp serve
  --addr          :8080                 Listen address          (env: DCP_LISTEN)
  --pg            postgres://...        PostgreSQL DSN          (env: DCP_PG_DSN)
  --redis         redis://localhost     Redis URL               (env: DCP_REDIS)
  --ssh-ca        /etc/dcp/ssh-ca/platform_ca
  --wg-privkey    /etc/wireguard/cp_private
  --wg-pubkey     /etc/wireguard/cp_public
  --wg-endpoint   127.0.0.1:51820      Endpoint sent to providers (env: DCP_WG_ENDPOINT)
  --jwt-secret    <secret>              Required                (env: DCP_JWT_SECRET)

dcp-cp token generate
  -n 1            Number of tokens
  --spire         Also generate a SPIRE join token for each
```

### `dcp-provider`

```
dcp-provider setup
  --cp-url        http://...    Control plane URL       (required)
  --token         <token>       DCP registration token  (required)
  --spire-token   <token>       SPIRE join token        (from dcp-cp token generate --spire)
  --cp-ip         127.0.0.1    CP IP for SPIRE agent
  --location      unknown       Location tag

dcp-provider [run]
  --cp-url        http://...    (env: DCP_CP_URL)
  --token         <token>       (env: DCP_TOKEN)
  --location      unknown       (env: DCP_LOCATION)
  --log-level     info
```

### `make` targets

```
make build              Build both binaries for local OS
make release            Cross-compile for linux/amd64 and linux/arm64
make install-cp         sudo bash scripts/install.sh cp
make install-provider   sudo bash scripts/install.sh provider
make install-both       sudo bash scripts/install.sh both
make deploy-cp          HOST=user@ip   Build + scp + restart dcp-cp
make deploy-provider    HOST=user@ip   Build + scp + restart dcp-provider
make health             Run health check script
make clean              Remove build artifacts
```

---

## File Locations

```
/etc/dcp/
  ssh-ca/platform_ca        SSH CA private key  ← most sensitive secret
  ssh-ca/platform_ca.pub    SSH CA public key   ← bake into every VM's sshd
  pg_pass                   PostgreSQL password
  pg_dsn                    Full PostgreSQL DSN
  jwt_secret                JWT signing secret
  provider.env              Provider env (DCP_CP_URL, DCP_TOKEN, DCP_LOCATION)

/etc/wireguard/
  cp_private / cp_public    Control plane WireGuard keypair
  prov_private / prov_public  Provider keypair (written by dcp-provider setup)
  wg0.conf                  Interface config (CP = relay, Provider = client)

/etc/spire/
  server.conf               SPIRE server config (CP only)
  agent.conf                SPIRE agent config (all nodes)
  join_token                Current bootstrap join token

/opt/dcp/kernels/vmlinux    Firecracker microVM kernel
/var/lib/dcp/vms/           Running VM state
/var/lib/dcp/rootfs/        VM root filesystem images
```

---

## Security Notes

- **SSH CA key** (`/etc/dcp/ssh-ca/platform_ca`) — move to HashiCorp Vault or AWS KMS in production. Anyone with this key can issue valid SSH certificates to any VM.
- **WireGuard** — providers never need a public IP. All SSH sessions tunnel through the CP relay. Provider machines only initiate outbound connections.
- **SPIRE SVIDs** expire every hour. Clock drift causes certificate rejections — `chrony` is non-negotiable.
- **Firecracker** — each VM runs with a separate kernel, network namespace, and jailer-enforced seccomp profile. Providers cannot read VM memory.
- **Redis and PostgreSQL** are bound to `127.0.0.1` only — never exposed on a public interface.
