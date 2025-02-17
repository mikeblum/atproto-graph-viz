package main

import (
	"context"
	"os"

	"github.com/mikeblum/atproto-graph-viz/bsky"
	"github.com/mikeblum/atproto-graph-viz/conf"
	"github.com/mikeblum/atproto-graph-viz/graph"
	"github.com/mikeblum/atproto-graph-viz/o11y"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
)

func main() {
	log := conf.NewLog()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var client *bsky.Client
	var err error

	// configure o11y
	var exporter *otlpmetricgrpc.Exporter
	if exporter, err = o11y.NewO11yLocal(ctx, log); err != nil {
		log.WithErrorMsg(err, "Error bootstrapping OTEL o11y", "mode", "local")
		exit()
	}
	defer func() {
		if err := exporter.Shutdown(ctx); err != nil {
			log.WithError(err).Error("failed to shutdown OTEL exporter")
		}
	}()

	var engine *graph.Engine
	if engine, err = graph.Bootstrap(ctx); err != nil {
		log.WithErrorMsg(err, "Error bootstrapping neo4j driver")
		exit()
	}

	defer engine.Close(ctx)

	// create indexes
	if err = engine.CreateIndexes(ctx); err != nil {
		log.WithErrorMsg(err, "Error creating indexes")
		exit()
	}

	// create constraints
	if err = engine.CreateConstraints(ctx); err != nil {
		log.WithErrorMsg(err, "Error creating constraints")
		exit()
	}

	if client, err = bsky.New(); err != nil {
		log.WithErrorMsg(err, "Error creating bsky client")
		exit()
	}

	// bootstrap worker pool
	pool := bsky.NewWorkerPool(client, bsky.NewConf()).StartMonitor(ctx).WithIngest(engine.Ingest)
	go func() {
		log.Info("Starting worker pool...")
		if err = pool.Start(ctx); err != nil {
			log.WithErrorMsg(err, "Error starting bsky worker pool")
			cancel() // cancel context if worker pool fails to start
		}
	}()

	// Wait for pool to be ready
	select {
	case <-pool.PoolReady():
		log.Info("Worker pool ready")
	case <-ctx.Done():
		log.WithErrorMsg(ctx.Err(), "Context cancelled before pool was ready")
		return
	}

	// Wait for ingest to be ready
	select {
	case <-pool.IngestReady():
		log.Info("Repo ingest ready")
	case <-ctx.Done():
		log.WithErrorMsg(ctx.Err(), "Context cancelled before ingest was ready")
		return
	}

	// Channel to signal backfill completion
	done := make(chan bool)

	// Start backfill in the background
	go func() {
		defer close(done)
		if err := client.BackfillRepos(ctx, pool); err != nil {
			log.WithErrorMsg(err, "Error backfilling bsky repos")
			cancel()
			return
		}
	}()

	// Await completion or cancellation
	select {
	case <-done:
		log.Info("Bsky backfill successful ✅")
	case <-ctx.Done():
		log.WithErrorMsg(ctx.Err(), "Error backfilling bsky repos ❌")
	}
}

func exit() {
	os.Exit(1)
}
