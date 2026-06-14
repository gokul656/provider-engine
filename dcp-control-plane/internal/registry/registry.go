package registry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dcp/control-plane/pkg/store"
	"github.com/google/uuid"
)

type Registry struct {
	pg    *store.PG
	redis *store.Redis
	wgPub string // CP WireGuard public key
}

func New(pg *store.PG, redis *store.Redis, cpWGPubKey string) *Registry {
	return &Registry{pg: pg, redis: redis, wgPub: cpWGPubKey}
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

	wgConf := buildWGConfig(req.WGPubKey, wgIP, r.wgPub)
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

// buildWGConfig generates the wg0.conf content to send to a provider.
func buildWGConfig(providerPubKey, providerIP, cpPubKey string) string {
	return fmt.Sprintf(`[Interface]
Address = %s/24
PrivateKey = <REPLACE_WITH_YOUR_PRIVATE_KEY>

[Peer]
PublicKey = %s
Endpoint = <CP_PUBLIC_IP>:51820
AllowedIPs = 10.99.0.0/24
PersistentKeepalive = 25
`, providerIP, cpPubKey)
}
