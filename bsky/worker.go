package bsky

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/repo"
	"github.com/bluesky-social/indigo/xrpc"
	log "github.com/mikeblum/atproto-graph-viz/conf"
	"golang.org/x/sync/errgroup"
)

type RepoJob struct {
	repo     *atproto.SyncListRepos_Repo
	workerID *int
}

type RepoJobOut struct {
	repo  *repo.Repo
	items chan RepoItem
}

type IngestJobOut struct {
	ID       string
	err      error
	workerID *int
}

type WorkerPool struct {
	client      *Client
	log         *log.Log
	jobs        chan RepoJob
	ingests     chan RepoJobOut
	poolReady   chan bool
	ingestReady chan bool
	done        chan bool
	rateLimiter *RateLimitHandler
	ingest      func(context.Context, chan RepoItem) error
	workerCount int
}

func NewWorkerPool(client *Client, conf *Conf) *WorkerPool {
	return &WorkerPool{
		client:      client,
		log:         log.NewLog(),
		jobs:        make(chan RepoJob, conf.WorkerCount()*2),
		ingests:     make(chan RepoJobOut, conf.WorkerCount()*2),
		poolReady:   make(chan bool),
		ingestReady: make(chan bool),
		done:        make(chan bool),
		rateLimiter: NewRateLimitHandler(client.atproto),
		workerCount: conf.WorkerCount(),
	}
}

func (p *WorkerPool) StartMonitor(ctx context.Context) *WorkerPool {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.log.Info("Worker pool status",
					"jobs_queued", len(p.jobs),
					"ingests_queued", len(p.ingests))
			}
		}
	}()
	return p
}

func (p *WorkerPool) PoolReady() chan bool {
	return p.poolReady
}

func (p *WorkerPool) IngestReady() chan bool {
	return p.ingestReady
}

func (p *WorkerPool) Size() int {
	return len(p.jobs)
}

func (p *WorkerPool) WithIngest(ingest func(context.Context, chan RepoItem) error) *WorkerPool {
	p.ingest = ingest
	return p
}

// Start - step #1: start worker pool
func (p *WorkerPool) Start(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	p.log.Info("Starting worker pool", "worker_count", p.workerCount)

	// Start repo workers with explicit worker IDs
	for i := 0; i < p.workerCount; i++ {
		workerID := i + 1
		g.Go(func() error {
			return p.repoWorker(ctx, workerID)
		})
	}

	// Start ingest workers with explicit worker IDs
	for i := 0; i < p.workerCount; i++ {
		workerID := i + 1
		g.Go(func() error {
			return p.ingestWorker(ctx, workerID)
		})
	}

	// Handle shutdown
	go func() {
		<-ctx.Done()
		close(p.done)
	}()

	// Signal pool is ready
	close(p.poolReady)
	// Signal ingest is ready
	close(p.ingestReady)

	return g.Wait()
}

// Submit - step #2: submit repo jobs for processing
func (p *WorkerPool) Submit(ctx context.Context, job RepoJob) error {
	if job.repo == nil {
		return fmt.Errorf("error submitting RepoJob: missing repo")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return fmt.Errorf("worker pool is shutting down")
	case p.jobs <- job: // block until more work can be processed
		return nil
	}
}

func (p *WorkerPool) ingestWorker(ctx context.Context, workerID int) error {
	p.log.Info("Worker started", "worker-type", "ingest", "worker-id", workerID)
	defer p.log.Info("Worker shutting down", "worker-type", "ingest", "worker-id", workerID)

	for {
		select {
		case <-ctx.Done():
			p.log.Info("Context cancelled", "worker-type", "ingest", "worker-id", workerID)
			return ctx.Err()
		case <-p.done:
			p.log.Info("Done channel closed", "worker-type", "ingest", "worker-id", workerID)
			return nil
		case ingest, ok := <-p.ingests:
			if !ok {
				p.log.Info("Ingest channel closed", "worker-type", "ingest", "worker-id", workerID)
				return nil
			}

			p.log.Info("Processing ingest",
				"action", "ingest",
				"worker-id", workerID,
				"did", ingest.repo.RepoDid())

			err := p.rateLimiter.withRetry(ctx, WriteOperation, "ingest", func() error {
				return p.ingest(ctx, ingest.items)
			})

			if err != nil {
				p.log.WithErrorMsg(err, "Retries exhausted",
					"action", "ingest",
					"worker-id", workerID,
					"did", ingest.repo.RepoDid())
			}
		}
	}
}

func (p *WorkerPool) repoWorker(ctx context.Context, workerID int) error {
	p.log.Info("Worker started", "type", "repo", "worker-id", workerID)
	defer p.log.Info("Worker shutting down", "type", "repo", "worker-id", workerID)

	for {
		select {
		case <-ctx.Done():
			p.log.Info("Context cancelled", "worker-id", workerID)
			return ctx.Err()
		case <-p.done:
			p.log.Info("Done channel closed", "worker-id", workerID)
			return nil
		case job, ok := <-p.jobs:
			if !ok {
				p.log.Info("Jobs channel closed", "worker-id", workerID)
				return nil
			}

			p.log.Info("Processing job",
				"action", "repo",
				"worker-id", workerID,
				"did", job.repo.Did)

			err := p.rateLimiter.withRetry(ctx, ReadOperation, "getRepo", func() error {
				if err := p.getRepo(ctx, job); err != nil {
					if !suppressATProtoErr(err) {
						p.log.WithErrorMsg(err, "Error getting repo",
							"worker-id", workerID,
							"did", job.repo.Did)
					}
					return err
				}
				return nil
			})

			if err != nil {
				p.log.WithErrorMsg(err, "Retries exhausted",
					"action", "repo",
					"worker-id", workerID,
					"did", job.repo.Did)
			}
		}
	}
}

func (p *WorkerPool) getRepo(ctx context.Context, job RepoJob) error {
	var repoData []byte
	var err error
	var ident *identity.Identity
	var atid *syntax.AtIdentifier
	if atid, err = syntax.ParseAtIdentifier(job.repo.Did); err != nil {
		return err
	}
	if ident, err = identity.DefaultDirectory().Lookup(ctx, *atid); err != nil {
		return err
	}
	xrpcc := xrpc.Client{
		Host: ident.PDSEndpoint(),
	}
	if xrpcc.Host == "" {
		return fmt.Errorf("no PDS endpoint for identity: %s", atid)
	}
	if repoData, err = atproto.SyncGetRepo(ctx, &xrpcc, ident.DID.String(), ""); err != nil {
		if !suppressATProtoErr(err) {
			p.log.WithErrorMsg(err, "Error fetching bsky repo")
		}
		return err
	}
	var r *repo.Repo
	if r, err = repo.ReadRepoFromCar(context.Background(), bytes.NewReader(repoData)); err != nil {
		p.log.WithErrorMsg(err, "Error reading bsky repo")
		return err
	}
	out := RepoJobOut{
		repo:  r,
		items: make(chan RepoItem, ITEMS_BUFFER),
	}

	// resolveLexicon can block if items > ITEMS_BUFFER
	if err := resolveLexicon(ctx, ident, out); err != nil {
		// Unwrap error to check if it's a LexiconError
		if unwrappedErr := errors.Unwrap(err); unwrappedErr != nil {
			var lexErr *LexiconError
			if errors.As(unwrappedErr, &lexErr) {
				p.log.With(
					"did", job.repo.Did,
					"lexicon-type", lexErr.nsid.Name()).Debug("Skipping due to lexicon error")
			}
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case p.ingests <- out:
		p.log.With("did", job.repo.Did).Info("Enqueued repo for ingestion")
	}
	return nil
}
