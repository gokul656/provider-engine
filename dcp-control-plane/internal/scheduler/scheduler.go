package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/dcp/control-plane/pkg/store"
	"github.com/google/uuid"
)

type Scheduler struct {
	pg    *store.PG
	redis *store.Redis
}

func New(pg *store.PG, redis *store.Redis) *Scheduler {
	return &Scheduler{pg: pg, redis: redis}
}

type DeployRequest struct {
	ImageURL  string
	CPUCores  int
	MemoryMB  int
	DiskGB    int
	SSHPubKey string
	Env       map[string]string
}

type DeployResult struct {
	JobID      string
	ProviderID string
}

// Submit finds the best provider and enqueues a deploy job.
func (s *Scheduler) Submit(ctx context.Context, req DeployRequest) (*DeployResult, error) {
	provider, err := s.pg.BestProvider(ctx, req.CPUCores, req.MemoryMB)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no provider available with %d CPU / %d MB RAM", req.CPUCores, req.MemoryMB)
	}
	if err != nil {
		return nil, fmt.Errorf("find provider: %w", err)
	}

	jobID := uuid.New().String()
	job := &store.Job{
		ID:         jobID,
		ProviderID: provider.ID,
		ImageURL:   req.ImageURL,
		CPUCores:   req.CPUCores,
		MemoryMB:   req.MemoryMB,
		DiskGB:     req.DiskGB,
		SSHPubKey:  req.SSHPubKey,
		Env:        req.Env,
	}
	if err := s.pg.InsertJob(ctx, job); err != nil {
		return nil, fmt.Errorf("insert job: %w", err)
	}
	if err := s.pg.AssignJob(ctx, jobID, provider.ID); err != nil {
		return nil, fmt.Errorf("assign job: %w", err)
	}
	// Wake up the provider via Redis queue
	if err := s.redis.PushJob(ctx, provider.ID, jobID); err != nil {
		return nil, fmt.Errorf("push job to redis: %w", err)
	}

	slog.Info("scheduler: job dispatched", "job", jobID, "provider", provider.ID)
	return &DeployResult{JobID: jobID, ProviderID: provider.ID}, nil
}

// PollForProvider is called by the provider's long-poll HTTP request.
// It blocks up to waitSec seconds for a job, then returns the job or nil.
func (s *Scheduler) PollForProvider(ctx context.Context, providerID string, waitSec int) (*store.Job, error) {
	jobID, err := s.redis.PopJob(ctx, providerID, waitSec)
	if err != nil {
		return nil, err
	}
	if jobID == "" {
		return nil, nil
	}
	return s.pg.NextPendingJob(ctx, providerID)
}
