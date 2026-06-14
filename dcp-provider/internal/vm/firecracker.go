package vm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	kernelPath = "/opt/dcp/kernels/vmlinux"
	jailerBin  = "/usr/local/bin/jailer"
	fcBin      = "/usr/local/bin/firecracker"
	vmBaseDir  = "/srv/dcp/vms"
)

// FirecrackerVM manages a single Firecracker microVM.
type FirecrackerVM struct {
	JobID    string
	ID       string
	socketPath string
	tapName    string
	vmIP       string
	pid        int
}

type SpawnConfig struct {
	JobID     string
	ImagePath string // path to root filesystem (ext4)
	CPUCores  int
	MemoryMB  int
	SSHPubKey string
}

// Spawn launches a jailer + firecracker microVM and returns the VM's tap IP.
func Spawn(ctx context.Context, cfg SpawnConfig) (*FirecrackerVM, error) {
	vmID := cfg.JobID
	vmDir := filepath.Join(vmBaseDir, vmID)
	if err := os.MkdirAll(vmDir, 0700); err != nil {
		return nil, err
	}

	socketPath := filepath.Join(vmDir, "api.sock")
	tapName := "tap-" + vmID[:8]
	vmIP, gwIP, err := allocateTapIP()
	if err != nil {
		return nil, err
	}

	// Create tap device
	if err := setupTap(tapName, gwIP); err != nil {
		return nil, fmt.Errorf("tap setup: %w", err)
	}

	// Start jailer → firecracker
	cmd := exec.CommandContext(ctx, jailerBin,
		"--id", vmID,
		"--exec-file", fcBin,
		"--uid", "1000",
		"--gid", "1000",
		"--",
		"--api-sock", socketPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("jailer start: %w", err)
	}

	vm := &FirecrackerVM{
		JobID:      cfg.JobID,
		ID:         vmID,
		socketPath: socketPath,
		tapName:    tapName,
		vmIP:       vmIP,
		pid:        cmd.Process.Pid,
	}

	// Wait for socket to appear (FC takes ~100ms)
	if err := waitForSocket(socketPath, 3*time.Second); err != nil {
		cmd.Process.Kill()
		return nil, err
	}

	if err := vm.configure(ctx, cfg); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("fc configure: %w", err)
	}

	slog.Info("firecracker: VM started", "job", cfg.JobID, "ip", vmIP)
	return vm, nil
}

func (v *FirecrackerVM) IP() string { return v.vmIP }

func (v *FirecrackerVM) Kill() {
	if v.pid > 0 {
		if p, err := os.FindProcess(v.pid); err == nil {
			p.Kill()
		}
	}
	exec.Command("ip", "link", "del", v.tapName).Run()
}

// configure calls the Firecracker API to set up the VM before boot.
func (v *FirecrackerVM) configure(ctx context.Context, cfg SpawnConfig) error {
	c := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", v.socketPath)
			},
		},
	}
	base := "http://localhost"

	// Boot source
	if err := fcPut(ctx, c, base+"/boot-source", map[string]any{
		"kernel_image_path": kernelPath,
		"boot_args":         "console=ttyS0 reboot=k panic=1 pci=off",
	}); err != nil {
		return err
	}

	// Root drive
	if err := fcPut(ctx, c, base+"/drives/rootfs", map[string]any{
		"drive_id":       "rootfs",
		"path_on_host":   cfg.ImagePath,
		"is_root_device": true,
		"is_read_only":   false,
	}); err != nil {
		return err
	}

	// Machine config
	if err := fcPut(ctx, c, base+"/machine-config", map[string]any{
		"vcpu_count":  cfg.CPUCores,
		"mem_size_mib": cfg.MemoryMB,
	}); err != nil {
		return err
	}

	// Network interface
	if err := fcPut(ctx, c, base+"/network-interfaces/eth0", map[string]any{
		"iface_id":       "eth0",
		"guest_mac":      "AA:FC:00:00:00:01",
		"host_dev_name":  v.tapName,
	}); err != nil {
		return err
	}

	// Boot
	return fcPut(ctx, c, base+"/actions", map[string]any{
		"action_type": "InstanceStart",
	})
}

func fcPut(ctx context.Context, c *http.Client, url string, body any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("fc API %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

func setupTap(name, gwIP string) error {
	cmds := [][]string{
		{"ip", "tuntap", "add", name, "mode", "tap"},
		{"ip", "addr", "add", gwIP + "/30", "dev", name},
		{"ip", "link", "set", name, "up"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %w: %s", args, err, out)
		}
	}
	return nil
}

var ipCounter = 1

func allocateTapIP() (vmIP, gwIP string, err error) {
	// Simple sequential allocation; replace with Redis-backed registry in prod
	ipCounter += 4
	base := ipCounter
	vmIP = fmt.Sprintf("172.16.%d.2", base)
	gwIP = fmt.Sprintf("172.16.%d.1", base)
	return
}

func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("socket %s did not appear within %s", path, timeout)
}

// KVMAvailable returns true if /dev/kvm exists and is accessible.
func KVMAvailable() bool {
	_, err := os.Stat("/dev/kvm")
	return err == nil
}

