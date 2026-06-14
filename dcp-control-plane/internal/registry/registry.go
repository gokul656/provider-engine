package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dcp/control-plane/pkg/store"
	"github.com/google/uuid"
)

// ErrInvalidToken is returned when a provider registers with a token that was
// never issued (or has been revoked).
var ErrInvalidToken = errors.New("invalid or unknown registration token")

type Registry struct {
	pg         *store.PG
	redis      *store.Redis
	wgPub      string // CP WireGuard public key
	cpEndpoint string // host:port providers connect to (e.g. "1.2.3.4:51820" or "127.0.0.1:51820")
}

func New(pg *store.PG, redis *store.Redis, cpWGPubKey, cpEndpoint string) *Registry {
	return &Registry{pg: pg, redis: redis, wgPub: cpWGPubKey, cpEndpoint: cpEndpoint}
}

type RegisterRequest struct {
	Token     string
	WGPubKey  string
	Location  string
	Mode      string
	CPUCores  int
	MemoryMB  int
	DiskGB    int
	MachineID string
	BinaryHash string
}

type RegisterResult struct {
	ProviderID string
	WGConfig   string // full wg0.conf to send back
	TunnelPort int
	WGIP       string
}

func (r *Registry) Register(ctx context.Context, req RegisterRequest) (*RegisterResult, error) {
	// Reject registration unless the token was issued via `dcp-cp token generate`.
	valid, err := r.redis.TokenValid(ctx, req.Token)
	if err != nil {
		return nil, fmt.Errorf("validate token: %w", err)
	}
	if !valid {
		return nil, ErrInvalidToken
	}

	wgIP, err := r.redis.AllocWGIP(ctx)
	if err != nil {
		return nil, fmt.Errorf("alloc wg ip: %w", err)
	}
	tunnelPort, err := r.redis.AllocPort(ctx)
	if err != nil {
		return nil, fmt.Errorf("alloc port: %w", err)
	}

	providerID := uuid.New().String()
	pr := &store.Provider{
		ID:           providerID,
		Token:        req.Token,
		WGPubKey:     req.WGPubKey,
		WGIP:         wgIP,
		TunnelPort:   tunnelPort,
		Location:     req.Location,
		Mode:         req.Mode,
		CPUCores:     req.CPUCores,
		MemoryMB:     req.MemoryMB,
		DiskGB:       req.DiskGB,
		MachineID:    req.MachineID,
		BinaryHash:   req.BinaryHash,
		Status:       store.ProviderOnline,
		RegisteredAt: time.Now(),
	}
	if err := r.pg.UpsertProvider(ctx, pr); err != nil {
		return nil, fmt.Errorf("upsert provider: %w", err)
	}

	wgConf := buildWGConfig(req.WGPubKey, wgIP, r.wgPub, r.cpEndpoint)
	slog.Info("registry: registered provider", "id", providerID, "ip", wgIP, "mode", req.Mode)
	return &RegisterResult{
		ProviderID: providerID,
		WGConfig:   wgConf,
		TunnelPort: tunnelPort,
		WGIP:       wgIP,
	}, nil
}

func (r *Registry) Heartbeat(ctx context.Context, id string, activeVMs int, cpuLoad float64, memFree int) error {
	return r.pg.UpdateHeartbeat(ctx, id, activeVMs, cpuLoad, memFree)
}

// SweepOffline marks providers as offline if not seen in the last 90 seconds.
func (r *Registry) SweepOffline(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := r.pg.MarkOffline(ctx, 90*time.Second)
			if err != nil {
				slog.Warn("sweep offline failed", "err", err)
			} else if n > 0 {
				slog.Info("swept offline providers", "count", n)
			}
		}
	}
}

// buildWGConfig generates the wg0.conf to send to a newly registered provider.
// Each provider is a standard WireGuard client with its own wg0.
// The CP is the only peer — providers never talk directly to each other.
// PrivateKey points to the path written by GenerateKeypair on the provider machine.
func buildWGConfig(providerPubKey, providerIP, cpPubKey, cpEndpoint string) string {
	_ = providerPubKey // the CP adds it as a peer via `wg set wg0 peer`, not in this conf
	return fmt.Sprintf(`[Interface]
Address = %s/32
PrivateKey = /etc/wireguard/prov_private

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = 10.99.0.0/16
PersistentKeepalive = 25
`, providerIP, cpPubKey, cpEndpoint)
}
