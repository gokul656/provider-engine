package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

type PG struct {
	db *sql.DB
}

func NewPG(dsn string) (*PG, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return &PG{db: db}, nil
}

func (p *PG) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS providers (
  id            TEXT PRIMARY KEY,
  token         TEXT NOT NULL,
  wg_pubkey     TEXT NOT NULL,
  wg_ip         TEXT NOT NULL,
  tunnel_port   INT  NOT NULL,
  location      TEXT NOT NULL,
  mode          TEXT NOT NULL,
  cpu_cores     INT  NOT NULL DEFAULT 0,
  memory_mb     INT  NOT NULL DEFAULT 0,
  disk_gb       INT  NOT NULL DEFAULT 0,
  machine_id    TEXT NOT NULL DEFAULT '',
  binary_hash   TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'online',
  active_vms    INT  NOT NULL DEFAULT 0,
  cpu_load      REAL NOT NULL DEFAULT 0,
  mem_free_mb   INT  NOT NULL DEFAULT 0,
  last_seen     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS vms (
  id          TEXT PRIMARY KEY,
  job_id      TEXT NOT NULL,
  provider_id TEXT NOT NULL REFERENCES providers(id),
  vm_ip       TEXT NOT NULL,
  port        INT  NOT NULL,
  status      TEXT NOT NULL DEFAULT 'running',
  error       TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS jobs (
  id          TEXT PRIMARY KEY,
  provider_id TEXT NOT NULL DEFAULT '',
  image_url   TEXT NOT NULL,
  cpu_cores   INT  NOT NULL,
  memory_mb   INT  NOT NULL,
  disk_gb     INT  NOT NULL DEFAULT 0,
  ssh_pubkey  TEXT NOT NULL,
  env         JSONB NOT NULL DEFAULT '{}',
  status      TEXT NOT NULL DEFAULT 'pending',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

func (p *PG) UpsertProvider(ctx context.Context, pr *Provider) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO providers
		  (id, token, wg_pubkey, wg_ip, tunnel_port, location, mode,
		   cpu_cores, memory_mb, disk_gb, machine_id, binary_hash, status, registered_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (id) DO UPDATE SET
		  wg_pubkey=EXCLUDED.wg_pubkey, status='online', last_seen=NOW()`,
		pr.ID, pr.Token, pr.WGPubKey, pr.WGIP, pr.TunnelPort,
		pr.Location, pr.Mode, pr.CPUCores, pr.MemoryMB, pr.DiskGB,
		pr.MachineID, pr.BinaryHash, string(ProviderOnline), pr.RegisteredAt,
	)
	return err
}

func (p *PG) UpdateHeartbeat(ctx context.Context, id string, activeVMs int, cpuLoad float64, memFree int) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE providers SET
		  last_seen=NOW(), active_vms=$2, cpu_load=$3, mem_free_mb=$4, status='online'
		WHERE id=$1`, id, activeVMs, cpuLoad, memFree)
	return err
}

func (p *PG) MarkOffline(ctx context.Context, olderThan time.Duration) (int64, error) {
	res, err := p.db.ExecContext(ctx, `
		UPDATE providers SET status='offline'
		WHERE status='online' AND last_seen < NOW() - $1::interval`,
		fmt.Sprintf("%d seconds", int(olderThan.Seconds())),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// BestProvider returns the online provider with most free memory that fits the job.
func (p *PG) BestProvider(ctx context.Context, cpuNeeded, memNeeded int) (*Provider, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, wg_ip, tunnel_port, mode
		FROM providers
		WHERE status='online'
		  AND cpu_cores >= $1
		  AND mem_free_mb >= $2
		ORDER BY mem_free_mb DESC
		LIMIT 1`, cpuNeeded, memNeeded)
	var pr Provider
	if err := row.Scan(&pr.ID, &pr.WGIP, &pr.TunnelPort, &pr.Mode); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (p *PG) InsertJob(ctx context.Context, j *Job) error {
	envJSON, _ := json.Marshal(j.Env)
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO jobs (id, image_url, cpu_cores, memory_mb, disk_gb, ssh_pubkey, env, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8)`,
		j.ID, j.ImageURL, j.CPUCores, j.MemoryMB, j.DiskGB, j.SSHPubKey, envJSON, j.CreatedAt,
	)
	return err
}

func (p *PG) AssignJob(ctx context.Context, jobID, providerID string) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE jobs SET provider_id=$2, status='scheduled' WHERE id=$1`, jobID, providerID)
	return err
}

// NextPendingJob returns the oldest pending job assigned to providerID, then marks it scheduled.
func (p *PG) NextPendingJob(ctx context.Context, providerID string) (*Job, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var j Job
	var envJSON []byte
	err = tx.QueryRowContext(ctx, `
		SELECT id, image_url, cpu_cores, memory_mb, disk_gb, ssh_pubkey, env
		FROM jobs
		WHERE provider_id=$1 AND status='scheduled'
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, providerID).
		Scan(&j.ID, &j.ImageURL, &j.CPUCores, &j.MemoryMB, &j.DiskGB, &j.SSHPubKey, &envJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(envJSON, &j.Env)

	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status='running' WHERE id=$1`, j.ID); err != nil {
		return nil, err
	}
	return &j, tx.Commit()
}

func (p *PG) UpsertVM(ctx context.Context, v *VM) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO vms (id, job_id, provider_id, vm_ip, port, status, error, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET
		  status=EXCLUDED.status, error=EXCLUDED.error`,
		v.ID, v.JobID, v.ProviderID, v.VMIP, v.Port, v.Status, v.Error, v.CreatedAt,
	)
	return err
}

func (p *PG) GetProvider(ctx context.Context, id string) (*Provider, error) {
	var pr Provider
	err := p.db.QueryRowContext(ctx,
		`SELECT id, wg_pubkey, wg_ip, tunnel_port, mode, status FROM providers WHERE id=$1`, id).
		Scan(&pr.ID, &pr.WGPubKey, &pr.WGIP, &pr.TunnelPort, &pr.Mode, &pr.Status)
	if err != nil {
		return nil, err
	}
	return &pr, nil
}

func (p *PG) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, location, mode, cpu_cores, memory_mb, status, active_vms, cpu_load, last_seen
		FROM providers ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var pr Provider
		if err := rows.Scan(&pr.ID, &pr.Location, &pr.Mode, &pr.CPUCores,
			&pr.MemoryMB, &pr.Status, &pr.ActiveVMs, &pr.CPULoad, &pr.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}
