package manager

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/converged-computing/fleetq/pkg/queue"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
)

// River makes a durable queue available behind the same Dispatcher seam,
// independent of which database backs it. The manager's schedule loop and the
// policies (pkg/policy) are unchanged: on a match it enqueues a durable, retried
// river dispatch job whose worker performs transform + Driver.Submit; the
// monitor loop reconciles from the store. river needs a SQL database, so the
// backend choice is Postgres (production) or SQLite (a local file / :memory:,
// no server). Pure in-memory has no river — that is queue.Memory + inline
// dispatch.

// dispatchArgs is the river payload: just the fleetq job id (the durable record
// lives in fleetq_jobs; the worker reloads it).
type dispatchArgs struct {
	JobID string `json:"job_id"`
}

func (dispatchArgs) Kind() string { return "fleetq_dispatch" }

type dispatchWorker struct {
	river.WorkerDefaults[dispatchArgs]
	m *Manager
}

func (w *dispatchWorker) Work(ctx context.Context, job *river.Job[dispatchArgs]) error {
	w.m.dispatchNow(job.Args.JobID)
	return nil
}

// riverInserter is the driver-agnostic slice of *river.Client we use. Both
// *river.Client[pgx.Tx] and *river.Client[*sql.Tx] satisfy it (Insert's
// signature doesn't mention the tx type), so the dispatcher doesn't care which
// database backs river.
type riverInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// RiverDispatcher implements Dispatcher by enqueuing a durable river job.
type RiverDispatcher struct{ ins riverInserter }

func (d *RiverDispatcher) Dispatch(j queue.Job) error {
	_, err := d.ins.Insert(context.Background(), dispatchArgs{JobID: j.ID}, nil)
	return err
}

func workersFor(m *Manager) *river.Workers {
	ws := river.NewWorkers()
	river.AddWorker(ws, &dispatchWorker{m: m})
	return ws
}

var riverQueues = map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}}

// NewRiverEnginePostgres wires river over Postgres (pgx). Pair with
// queue.NewPostgres on the same pool.
func NewRiverEnginePostgres(ctx context.Context, m *Manager, pool *pgxpool.Pool) (*RiverDispatcher, func(context.Context) error, error) {
	drv := riverpgxv5.New(pool)
	migrator, err := rivermigrate.New(drv, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("river(pg) migrate init: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, nil, fmt.Errorf("river(pg) migrate up: %w", err)
	}
	client, err := river.NewClient(drv, &river.Config{Queues: riverQueues, Workers: workersFor(m)})
	if err != nil {
		return nil, nil, fmt.Errorf("river(pg) client: %w", err)
	}
	if err := client.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("river(pg) start: %w", err)
	}
	return &RiverDispatcher{ins: client}, client.Stop, nil
}

// NewRiverEngineSQLite wires river over SQLite (a local file or :memory:). Pair
// with queue.NewSQLite on the same *sql.DB. No server required.
func NewRiverEngineSQLite(ctx context.Context, m *Manager, db *sql.DB) (*RiverDispatcher, func(context.Context) error, error) {
	drv := riversqlite.New(db)
	migrator, err := rivermigrate.New(drv, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("river(sqlite) migrate init: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, nil, fmt.Errorf("river(sqlite) migrate up: %w", err)
	}
	client, err := river.NewClient(drv, &river.Config{Queues: riverQueues, Workers: workersFor(m)})
	if err != nil {
		return nil, nil, fmt.Errorf("river(sqlite) client: %w", err)
	}
	if err := client.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("river(sqlite) start: %w", err)
	}
	return &RiverDispatcher{ins: client}, client.Stop, nil
}
