package vm

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// ContainerVM wraps a Docker container as a VM fallback (no KVM required).
type ContainerVM struct {
	JobID       string
	containerID string
	ip          string
}

type ContainerConfig struct {
	JobID     string
	ImageRef  string
	CPUCores  int
	MemoryMB  int
	SSHPubKey string
}

// SpawnContainer starts a privileged Docker container and returns it.
func SpawnContainer(ctx context.Context, cfg ContainerConfig) (*ContainerVM, error) {
	args := []string{
		"run", "-d",
		"--name", "dcp-" + cfg.JobID,
		"--cpus", fmt.Sprintf("%d", cfg.CPUCores),
		"--memory", fmt.Sprintf("%dm", cfg.MemoryMB),
		"--label", "dcp.job=" + cfg.JobID,
		// SSH pubkey injected via env; init script in image should write it
		"--env", "DCP_SSH_PUBKEY=" + cfg.SSHPubKey,
		cfg.ImageRef,
	}
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	id := strings.TrimSpace(string(out))

	ip, err := containerIP(ctx, id)
	if err != nil {
		exec.Command("docker", "rm", "-f", id).Run()
		return nil, err
	}

	slog.Info("container: started", "job", cfg.JobID, "id", id[:12], "ip", ip)
	return &ContainerVM{JobID: cfg.JobID, containerID: id, ip: ip}, nil
}

func (c *ContainerVM) IP() string { return c.ip }

func (c *ContainerVM) Kill() {
	exec.Command("docker", "rm", "-f", c.containerID).Run()
}

func containerIP(ctx context.Context, id string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", id).Output()
	if err != nil {
		return "", fmt.Errorf("docker inspect: %w", err)
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("container %s has no IP", id[:12])
	}
	return ip, nil
}
