package store

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Redis struct {
	c *redis.Client
}

func NewRedis(addr string) (*Redis, error) {
	opt, err := redis.ParseURL(addr)
	if err != nil {
		return nil, fmt.Errorf("redis parse url: %w", err)
	}
	c := redis.NewClient(opt)
	if err := c.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Redis{c: c}, nil
}

// PendingJobKey is a list per provider — scheduler pushes, provider pops via long-poll.
func (r *Redis) PushJob(ctx context.Context, providerID, jobID string) error {
	return r.c.LPush(ctx, jobQueueKey(providerID), jobID).Err()
}

// PopJob blocks up to waitSec for a job for this provider (long-poll).
func (r *Redis) PopJob(ctx context.Context, providerID string, waitSec int) (string, error) {
	res, err := r.c.BRPop(ctx, time.Duration(waitSec)*time.Second, jobQueueKey(providerID)).Result()
	if err == redis.Nil {
		return "", nil // timeout, no job
	}
	if err != nil {
		return "", err
	}
	return res[1], nil // res[0] = key name, res[1] = value
}

// AllocPort returns a unique SSH relay port for a new VM (simple incrementing counter).
func (r *Redis) AllocPort(ctx context.Context) (int, error) {
	n, err := r.c.Incr(ctx, "dcp:port_counter").Result()
	if err != nil {
		return 0, err
	}
	// Ports 40000–50000 reserved for VM relay
	port := int(n%10000) + 40000
	return port, nil
}

// AllocWGIP returns the next available WireGuard IP for a provider (10.99.1.x).
func (r *Redis) AllocWGIP(ctx context.Context) (string, error) {
	n, err := r.c.Incr(ctx, "dcp:wg_ip_counter").Result()
	if err != nil {
		return "", err
	}
	// 10.99.1.1 – 10.99.1.254; wrap on overflow
	octet := (int(n) % 254) + 1
	return fmt.Sprintf("10.99.1.%d", octet), nil
}

func jobQueueKey(providerID string) string {
	return "dcp:jobs:" + providerID
}
