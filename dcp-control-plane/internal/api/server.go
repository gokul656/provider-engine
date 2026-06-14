package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dcp/control-plane/internal/registry"
	"github.com/dcp/control-plane/internal/scheduler"
	"github.com/dcp/control-plane/internal/sshca"
	"github.com/dcp/control-plane/internal/wgrelay"
	"github.com/dcp/control-plane/pkg/store"
)

type Config struct {
	ListenAddr string
	RedisAddr  string
	PGDSN      string
	SSHCAPath  string
	WGPrivKey  string
	WGPubKey   string
	JWTSecret  string
}

func Serve(cfg Config) error {
	ctx := context.Background()

	pg, err := store.NewPG(cfg.PGDSN)
	if err != nil {
		return err
	}
	if err := pg.Migrate(ctx); err != nil {
		return err
	}

	rdb, err := store.NewRedis(cfg.RedisAddr)
	if err != nil {
		return err
	}

	ca, err := sshca.Load(cfg.SSHCAPath)
	if err != nil {
		return err
	}

	cpPubKey, err := wgrelay.PubKey(cfg.WGPubKey)
	if err != nil {
		return err
	}

	if err := wgrelay.Init(wgrelay.Config{
		PrivKeyPath: cfg.WGPrivKey,
		PubKeyPath:  cfg.WGPubKey,
		ListenPort:  51820,
		Address:     "10.99.0.1/24",
	}); err != nil {
		slog.Warn("wgrelay init failed (non-fatal on non-Linux)", "err", err)
	}

	reg := registry.New(pg, rdb, cpPubKey)
	sched := scheduler.New(pg, rdb)
	go reg.SweepOffline(ctx)

	mux := http.NewServeMux()
	h := &handlers{reg: reg, sched: sched, ca: ca, pg: pg}

	// Provider-facing routes (token auth)
	mux.HandleFunc("POST /api/v1/providers/register", h.register)
	mux.HandleFunc("POST /api/v1/providers/heartbeat", h.heartbeat)
	mux.HandleFunc("GET /api/v1/providers/{id}/jobs", h.pollJobs)
	mux.HandleFunc("POST /api/v1/vms/report", h.reportVM)

	// Buyer-facing routes (JWT auth)
	mux.HandleFunc("POST /api/v1/deploy", withJWT(cfg.JWTSecret, h.deploy))
	mux.HandleFunc("POST /api/v1/ssh-cert", withJWT(cfg.JWTSecret, h.issueCert))

	// Admin / observability
	mux.HandleFunc("GET /api/v1/providers", withJWT(cfg.JWTSecret, h.listProviders))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	slog.Info("api: listening", "addr", cfg.ListenAddr)
	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      logging(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 35 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return srv.ListenAndServe()
}

type handlers struct {
	reg   *registry.Registry
	sched *scheduler.Scheduler
	ca    *sshca.CA
	pg    *store.PG
}

// ── Provider routes ───────────────────────────────────────────────────────────

func (h *handlers) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token     string            `json:"token"`
		WGPubKey  string            `json:"wg_pubkey"`
		Location  string            `json:"location"`
		Mode      string            `json:"mode"`
		Resources struct {
			CPUCores int `json:"cpu_cores"`
			MemoryMB int `json:"memory_mb"`
			DiskGB   int `json:"disk_gb"`
		} `json:"resources"`
		Meta map[string]string `json:"meta"`
	}
	if !decode(w, r, &req) {
		return
	}

	result, err := h.reg.Register(r.Context(), registry.RegisterRequest{
		Token:      req.Token,
		WGPubKey:   req.WGPubKey,
		Location:   req.Location,
		Mode:       req.Mode,
		CPUCores:   req.Resources.CPUCores,
		MemoryMB:   req.Resources.MemoryMB,
		DiskGB:     req.Resources.DiskGB,
		MachineID:  req.Meta["machine_id"],
		BinaryHash: req.Meta["binary_hash"],
	})
	if err != nil {
		httpErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Add provider as WireGuard peer on the CP
	if err := wgrelay.AddPeer(req.WGPubKey, result.WGIP); err != nil {
		slog.Warn("add wg peer failed (non-fatal)", "err", err)
	}

	respond(w, map[string]any{
		"provider_id": result.ProviderID,
		"wg_config":   result.WGConfig,
		"tunnel_port": result.TunnelPort,
	})
}

func (h *handlers) heartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProviderID string  `json:"provider_id"`
		ActiveVMs  int     `json:"active_vms"`
		CPULoad    float64 `json:"cpu_load"`
		MemFreeMB  int     `json:"mem_free_mb"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := h.reg.Heartbeat(r.Context(), req.ProviderID, req.ActiveVMs, req.CPULoad, req.MemFreeMB); err != nil {
		httpErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) pollJobs(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("id")
	job, err := h.sched.PollForProvider(r.Context(), providerID, 25)
	if err != nil {
		httpErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	respond(w, map[string]any{
		"job_id":     job.ID,
		"image_url":  job.ImageURL,
		"cpu_cores":  job.CPUCores,
		"memory_mb":  job.MemoryMB,
		"disk_gb":    job.DiskGB,
		"ssh_pubkey": job.SSHPubKey,
		"env":        job.Env,
	})
}

func (h *handlers) reportVM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		JobID      string `json:"job_id"`
		ProviderID string `json:"provider_id"`
		VMIP       string `json:"vm_ip"`
		Port       int    `json:"port"`
		Status     string `json:"status"`
		Error      string `json:"error"`
	}
	if !decode(w, r, &req) {
		return
	}
	vm := &store.VM{
		ID:         req.JobID, // 1:1 for now
		JobID:      req.JobID,
		ProviderID: req.ProviderID,
		VMIP:       req.VMIP,
		Port:       req.Port,
		Status:     req.Status,
		Error:      req.Error,
		CreatedAt:  time.Now(),
	}
	if err := h.pg.UpsertVM(r.Context(), vm); err != nil {
		httpErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("vm reported", "job", req.JobID, "status", req.Status, "ip", req.VMIP)
	w.WriteHeader(http.StatusNoContent)
}

// ── Buyer routes ──────────────────────────────────────────────────────────────

func (h *handlers) deploy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ImageURL  string            `json:"image_url"`
		CPUCores  int               `json:"cpu_cores"`
		MemoryMB  int               `json:"memory_mb"`
		DiskGB    int               `json:"disk_gb"`
		SSHPubKey string            `json:"ssh_pubkey"`
		Env       map[string]string `json:"env"`
	}
	if !decode(w, r, &req) {
		return
	}
	result, err := h.sched.Submit(r.Context(), scheduler.DeployRequest{
		ImageURL:  req.ImageURL,
		CPUCores:  req.CPUCores,
		MemoryMB:  req.MemoryMB,
		DiskGB:    req.DiskGB,
		SSHPubKey: req.SSHPubKey,
		Env:       req.Env,
	})
	if err != nil {
		httpErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	respond(w, result)
}

func (h *handlers) issueCert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserPubKey string `json:"user_pubkey"`
		Principal  string `json:"principal"`
		VMID       string `json:"vm_id"`
		TTLHours   int    `json:"ttl_hours"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Principal == "" {
		req.Principal = "root"
	}
	ttl := time.Duration(req.TTLHours) * time.Hour
	cert, err := h.ca.IssueCert(sshca.SignRequest{
		UserPubKey: req.UserPubKey,
		Principal:  req.Principal,
		VMID:       req.VMID,
		TTL:        ttl,
	})
	if err != nil {
		httpErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	respond(w, map[string]string{"certificate": cert})
}

func (h *handlers) listProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.pg.ListProviders(r.Context())
	if err != nil {
		httpErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	respond(w, providers)
}

// ── Middleware ────────────────────────────────────────────────────────────────

func withJWT(secret string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			httpErr(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		// Minimal HMAC-SHA256 JWT check — swap for a proper library in prod
		token := strings.TrimPrefix(auth, "Bearer ")
		if !validateJWT(token, secret) {
			httpErr(w, "invalid token", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpErr(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
