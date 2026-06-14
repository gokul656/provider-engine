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
}

func Run(cfg Config) error {
	setupLogger(cfg.LogLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Detect mode
	mode := "container"
	if vm.KVMAvailable() {
		mode = "firecracker"
	}
	slog.Info("agent: detected mode", "mode", mode)

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
		go handleJob(ctx, client, job, mode, regResp.TunnelPort)
	}
}

func handleJob(ctx context.Context, client *api.Client, job *api.DeployJob, mode string, tunnelPort int) {
	var vmIP string
	var spawnErr error

	if mode == "firecracker" {
		v, err := vm.Spawn(ctx, vm.SpawnConfig{
			JobID:     job.JobID,
			ImagePath: job.ImageURL, // assumed to be a local path after image pull
			CPUCores:  job.CPUCores,
			MemoryMB:  job.MemoryMB,
			SSHPubKey: job.SSHPubKey,
		})
		if err != nil {
			spawnErr = err
		} else {
			vmIP = v.IP()
		}
	} else {
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
		}
	}

	report := api.VMReport{
		JobID:      job.JobID,
		ProviderID: client.ProviderID(),
		Port:       tunnelPort,
	}
	if spawnErr != nil {
		slog.Error("spawn failed", "job", job.JobID, "err", spawnErr)
		report.Status = "failed"
		report.Error = spawnErr.Error()
	} else {
		report.Status = "running"
		report.VMIP = vmIP
		// Start SSH proxy so CP can reach the VM
		go func() {
			if err := tunnel.Proxy(tunnelPort, vmIP); err != nil {
				slog.Error("tunnel error", "err", err)
			}
		}()
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
