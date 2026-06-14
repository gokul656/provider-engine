package agent

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/dcp/provider-agent/internal/attestation"
	"github.com/dcp/provider-agent/internal/tunnel"
	"github.com/dcp/provider-agent/internal/vm"
	"github.com/dcp/provider-agent/internal/wireguard"
	"github.com/dcp/provider-agent/pkg/api"
)

type Config struct {
	ControlPlaneURL string
	Token           string
	Location        string
	LogLevel        string
	Mode            string // "auto" | "firecracker" | "container" | "ssh"
	SSHPort         int    // only used in ssh mode
}

func Run(cfg Config) error {
	setupLogger(cfg.LogLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Resolve mode
	mode := cfg.Mode
	if mode == "auto" || mode == "" {
		switch {
		case vm.KVMAvailable():
			mode = "firecracker"
		case isAndroid():
			mode = "ssh"
		default:
			mode = "container"
		}
	}
	slog.Info("agent: mode", "mode", mode)

	// 2. WireGuard keypair
	privKey, pubKey, err := wireguard.GenerateKeypair()
	if err != nil {
		return err
	}
	_ = privKey // stored in wg0.conf received from CP

	// 3. Attestation
	attest, err := attestation.Collect()
	if err != nil {
		slog.Warn("attestation failed (non-fatal)", "err", err)
	}

	// 4. Register with control plane
	client := api.NewClient(cfg.ControlPlaneURL, cfg.Token)
	regResp, err := client.Register(ctx, api.RegisterRequest{
		Token:     cfg.Token,
		PublicKey: pubKey,
		Resources: resources(),
		Location:  cfg.Location,
		Mode:      mode,
		Meta: map[string]string{
			"machine_id":  attest.MachineID,
			"binary_hash": attest.BinaryHash,
			"os":          runtime.GOOS,
			"arch":        runtime.GOARCH,
		},
	})
	if err != nil {
		return err
	}
	client.SetProviderID(regResp.ProviderID)
	slog.Info("agent: registered", "provider_id", regResp.ProviderID)

	// 5. Apply WireGuard config from CP
	if err := wireguard.WriteConfig(regResp.WGConfig); err != nil {
		return err
	}

	// 6. Heartbeat goroutine
	go heartbeat(ctx, client)

	// 7. Job loop
	slog.Info("agent: ready, waiting for deploy jobs")
	for {
		select {
		case <-ctx.Done():
			slog.Info("agent: shutting down")
			return nil
		default:
		}

		job, err := client.PollJobs(ctx)
		if err != nil {
			slog.Warn("poll error (will retry)", "err", err)
			sleep(ctx, 5*time.Second)
			continue
		}
		if job == nil {
			continue // long-poll timeout, loop
		}

		slog.Info("agent: received job", "job_id", job.JobID)
		go handleJob(ctx, client, job, mode, regResp.TunnelPort, cfg.SSHPort, regResp.WGIP)
	}
}

func handleJob(ctx context.Context, client *api.Client, job *api.DeployJob, mode string, tunnelPort, sshPort int, wgIP string) {
	var vmIP string
	var reportPort int
	var spawnErr error

	switch mode {
	case "firecracker":
		v, err := vm.Spawn(ctx, vm.SpawnConfig{
			JobID:     job.JobID,
			ImagePath: job.ImageURL,
			CPUCores:  job.CPUCores,
			MemoryMB:  job.MemoryMB,
			SSHPubKey: job.SSHPubKey,
		})
		if err != nil {
			spawnErr = err
		} else {
			vmIP = v.IP()
			reportPort = tunnelPort
		}

	case "container":
		v, err := vm.SpawnContainer(ctx, vm.ContainerConfig{
			JobID:     job.JobID,
			ImageRef:  job.ImageURL,
			CPUCores:  job.CPUCores,
			MemoryMB:  job.MemoryMB,
			SSHPubKey: job.SSHPubKey,
		})
		if err != nil {
			spawnErr = err
		} else {
			vmIP = v.IP()
			reportPort = tunnelPort
		}

	case "ssh":
		// Device itself is the compute — no VM spawned.
		// Buyer SSHs directly into this device over WireGuard.
		v, err := vm.SpawnSSH(ctx, vm.SSHConfig{
			JobID:       job.JobID,
			WireGuardIP: wgIP,
			SSHPort:     sshPort,
			SSHPubKey:   job.SSHPubKey,
		})
		if err != nil {
			spawnErr = err
		} else {
			vmIP = v.IP()
			reportPort = v.Port()
		}
	}

	report := api.VMReport{
		JobID:      job.JobID,
		ProviderID: client.ProviderID(),
		Port:       reportPort,
	}
	if spawnErr != nil {
		slog.Error("spawn failed", "job", job.JobID, "err", spawnErr)
		report.Status = "failed"
		report.Error = spawnErr.Error()
	} else {
		report.Status = "running"
		report.VMIP = vmIP
		// In ssh mode the buyer connects directly over WireGuard — no proxy needed.
		if mode != "ssh" {
			go func() {
				// Bind the relay to the provider's WireGuard IP so it's only
				// reachable by the CP over the tunnel, not the public interface.
				if err := tunnel.Proxy(wgIP, tunnelPort, vmIP); err != nil {
					slog.Error("tunnel error", "err", err)
				}
			}()
		}
	}

	if err := client.ReportVM(ctx, report); err != nil {
		slog.Error("report VM failed", "err", err)
	}
}

func heartbeat(ctx context.Context, client *api.Client) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := client.Heartbeat(ctx, api.HeartbeatRequest{
				ProviderID: client.ProviderID(),
				ActiveVMs:  0, // TODO: track active count
				CPULoad:    cpuLoad(),
				MemFreeMB:  memFreeMB(),
			})
			if err != nil {
				slog.Warn("heartbeat failed", "err", err)
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func setupLogger(level string) {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}

func isAndroid() bool {
	// ANDROID_ROOT is set in every Termux environment
	return os.Getenv("ANDROID_ROOT") != ""
}
