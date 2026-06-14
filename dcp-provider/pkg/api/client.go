package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	providerID string
	http       *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) SetProviderID(id string) { c.providerID = id }
func (c *Client) ProviderID() string      { return c.providerID }

// RegisterRequest is sent on first boot.
type RegisterRequest struct {
	Token     string            `json:"token"`
	PublicKey string            `json:"wg_pubkey"`
	Resources ResourceInfo      `json:"resources"`
	Location  string            `json:"location"`
	Mode      string            `json:"mode"` // "firecracker" | "container"
	Meta      map[string]string `json:"meta,omitempty"`
}

type ResourceInfo struct {
	CPUCores  int `json:"cpu_cores"`
	MemoryMB  int `json:"memory_mb"`
	DiskGB    int `json:"disk_gb"`
}

// RegisterResponse is returned by POST /api/v1/providers/register.
type RegisterResponse struct {
	ProviderID string `json:"provider_id"`
	WGConfig   string `json:"wg_config"`   // full wg0.conf content
	TunnelPort int    `json:"tunnel_port"` // SSH relay port on CP
}

func (c *Client) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {
	var resp RegisterResponse
	if err := c.post(ctx, "/api/v1/providers/register", req, &resp); err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	return &resp, nil
}

// HeartbeatRequest is sent every 30 s.
type HeartbeatRequest struct {
	ProviderID string  `json:"provider_id"`
	ActiveVMs  int     `json:"active_vms"`
	CPULoad    float64 `json:"cpu_load"`
	MemFreeMB  int     `json:"mem_free_mb"`
}

func (c *Client) Heartbeat(ctx context.Context, req HeartbeatRequest) error {
	return c.post(ctx, "/api/v1/providers/heartbeat", req, nil)
}

// DeployJob is received when the CP wants us to spin up a VM.
type DeployJob struct {
	JobID       string            `json:"job_id"`
	ImageURL    string            `json:"image_url"`
	CPUCores    int               `json:"cpu_cores"`
	MemoryMB    int               `json:"memory_mb"`
	DiskGB      int               `json:"disk_gb"`
	SSHPubKey   string            `json:"ssh_pubkey"`
	Env         map[string]string `json:"env,omitempty"`
}

// ReportVM reports a running VM back to the control plane.
type VMReport struct {
	JobID      string `json:"job_id"`
	ProviderID string `json:"provider_id"`
	VMIP       string `json:"vm_ip"`
	Port       int    `json:"port"`
	Status     string `json:"status"` // "running" | "failed"
	Error      string `json:"error,omitempty"`
}

func (c *Client) ReportVM(ctx context.Context, r VMReport) error {
	return c.post(ctx, "/api/v1/vms/report", r, nil)
}

// PollJobs long-polls for a deploy job assigned to this provider.
func (c *Client) PollJobs(ctx context.Context) (*DeployJob, error) {
	url := fmt.Sprintf("%s/api/v1/providers/%s/jobs?wait=25", c.baseURL, c.providerID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil // no job yet
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("poll jobs: HTTP %d", resp.StatusCode)
	}
	var job DeployJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, err
	}
	return &job, nil
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, path)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) setHeaders(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+c.token)
}
