package store

import "time"

type ProviderStatus string

const (
	ProviderOnline  ProviderStatus = "online"
	ProviderOffline ProviderStatus = "offline"
)

// Provider represents a registered compute provider.
type Provider struct {
	ID          string         `db:"id"`
	Token       string         `db:"token"`
	WGPubKey    string         `db:"wg_pubkey"`
	WGIP        string         `db:"wg_ip"`    // assigned IP on 10.99.0.0/24
	TunnelPort  int            `db:"tunnel_port"`
	Location    string         `db:"location"`
	Mode        string         `db:"mode"` // "firecracker" | "container"
	CPUCores    int            `db:"cpu_cores"`
	MemoryMB    int            `db:"memory_mb"`
	DiskGB      int            `db:"disk_gb"`
	MachineID   string         `db:"machine_id"`
	BinaryHash  string         `db:"binary_hash"`
	Status      ProviderStatus `db:"status"`
	ActiveVMs   int            `db:"active_vms"`
	CPULoad     float64        `db:"cpu_load"`
	MemFreeMB   int            `db:"mem_free_mb"`
	LastSeen    time.Time      `db:"last_seen"`
	RegisteredAt time.Time     `db:"registered_at"`
}

// VM represents a running workload on a provider.
type VM struct {
	ID         string    `db:"id"`
	JobID      string    `db:"job_id"`
	ProviderID string    `db:"provider_id"`
	VMIP       string    `db:"vm_ip"`
	Port       int       `db:"port"`
	Status     string    `db:"status"` // "running" | "failed" | "stopped"
	Error      string    `db:"error"`
	CreatedAt  time.Time `db:"created_at"`
}

// Job is a pending deploy request.
type Job struct {
	ID         string            `db:"id"`
	ProviderID string            `db:"provider_id"` // assigned provider, empty = unscheduled
	ImageURL   string            `db:"image_url"`
	CPUCores   int               `db:"cpu_cores"`
	MemoryMB   int               `db:"memory_mb"`
	DiskGB     int               `db:"disk_gb"`
	SSHPubKey  string            `db:"ssh_pubkey"`
	Env        map[string]string `db:"-"` // serialized separately
	Status     string            `db:"status"` // "pending" | "scheduled" | "running" | "failed"
	CreatedAt  time.Time         `db:"created_at"`
}
