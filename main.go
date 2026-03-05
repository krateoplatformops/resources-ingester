package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krateoplatformops/plumbing/kubeutil"
	"github.com/krateoplatformops/resources-ingester/internal/batch"
	"github.com/krateoplatformops/resources-ingester/internal/config"
	"github.com/krateoplatformops/resources-ingester/internal/queue"
	"github.com/krateoplatformops/resources-ingester/internal/router"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	crdGroupVersion = "apiextensions.k8s.io/v1"
	crdResourceName = "customresourcedefinitions"
)

func main() {
	cfg := config.Setup()

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	router.StartCacheCleaner(rootCtx, 2*time.Minute)

	/*pgCtx, cancel := context.WithTimeout(rootCtx, cfg.DbReadyTimeout)
	defer cancel()

	pool, err := pgutil.WaitForPostgres(pgCtx, cfg.Log, cfg.DbURL)
	if err != nil {
		cfg.Log.Error("cannot connect to PostgreSQL", slog.Any("err", err))
		os.Exit(1)
	}
	defer pool.Close()
	cfg.Log.Info("PostgreSQL is ready.")

	// Health probes server
	hs := probes.New(cfg.Log, pool, cfg.Port)
	hs.Start()*/

	pool := &pgxpool.Pool{} // Mock database, not used yet

	restConfig, err := rest.InClusterConfig()
	if err != nil {
		cfg.Log.Error("cannot get in-cluster config", slog.Any("err", err))
		os.Exit(1)
	}

	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		cfg.Log.Error("cannot create k8s client", slog.Any("err", err))
		os.Exit(1)
	}

	clusterName := kubeutil.DetectClusterName(restConfig)
	cfg.Log.Info("cluster name detected", slog.String("cluster", clusterName))

	// Record channel and batch worker
	recordChan := make(chan batch.InsertRecord, 100)
	batchWorker := batch.NewWorker(batch.WorkerOpts{
		Pool:       pool,
		Log:        cfg.Log,
		Input:      recordChan,
		MaxBatch:   5,
		FlushEvery: 1 * time.Second,
	})
	go batchWorker.Run(rootCtx.Done())

	// Queue with worker pool
	jobQueue := queue.NewQueue(1000, 4)
	jobQueue.Run()
	defer jobQueue.Terminate()

	// Ingester
	ing, err := router.NewIngester(router.IngesterOpts{
		RESTConfig:  restConfig,
		Queue:       jobQueue,
		Pool:        pool,
		Log:         cfg.Log,
		RecordChan:  recordChan,
		ClusterName: clusterName,
	})
	if err != nil {
		cfg.Log.Error("cannot create ingester", slog.Any("err", err))
		os.Exit(1)
	}

	// EventRouter
	crdsRouter := router.NewRouter(router.RouterOpts{
		DynamicClient:  client,
		Log:            cfg.Log,
		Handler:        ing,
		ResyncInterval: 30 * time.Second, // TODO make configurable
		ThrottlePeriod: 5 * time.Minute,  // TODO make configurable
		Namespaces:     []string{},
		GroupVersion:   crdGroupVersion,
		Resource:       crdResourceName,
	})
	go crdsRouter.Run(rootCtx.Done())

	// Monitor buffer
	go func() {
		ticker := time.NewTicker(50 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-rootCtx.Done():
				return
			case <-ticker.C:
				cfg.Log.Info("Pipeline status",
					slog.Int("recordChan", len(recordChan)),
					slog.Int("queueJobs", jobQueue.GetJobCount()),
				)
			}
		}
	}()

	cfg.Log.Info("Event ingester started")

	<-rootCtx.Done()
	cfg.Log.Info("Shutting down Event ingester")

	/*shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

		if err := hs.Shutdown(shutdownCtx); err != nil {
			cfg.Log.Error("Health server shutdown failed", slog.Any("err", err))
		}
	*/
}
