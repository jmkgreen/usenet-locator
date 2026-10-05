package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/articles"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/database"
	"github.com/jmkgreen/usenet-locator/backend/internal/httpapi"
	"github.com/jmkgreen/usenet-locator/backend/internal/indexing"
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
	"github.com/jmkgreen/usenet-locator/backend/internal/providers"
	"github.com/jmkgreen/usenet-locator/backend/internal/qualification"
	"github.com/jmkgreen/usenet-locator/backend/internal/retention"
	"github.com/jmkgreen/usenet-locator/backend/internal/retrieval"
	"github.com/jmkgreen/usenet-locator/backend/internal/timeline"
	"github.com/jmkgreen/usenet-locator/backend/internal/watchlist"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	configPath := os.Getenv("USENET_LOCATOR_CONFIG")
	if configPath == "" {
		logger.Error("configuration path is required", "environment", "USENET_LOCATOR_CONFIG")
		os.Exit(1)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("configuration invalid", "error", err)
		os.Exit(1)
	}
	databaseURL, err := config.ReadSecretFile(cfg.Database.URLFile)
	if err != nil {
		logger.Error("database URL secret is unavailable", "path", cfg.Database.URLFile, "error", err)
		os.Exit(1)
	}
	db, err := database.Open(context.Background(), databaseURL, cfg.Database.MaxConns)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	if err := db.SyncConfiguration(context.Background(), cfg); err != nil {
		logger.Error("database configuration sync failed", "error", err)
		os.Exit(1)
	}
	jobStore := jobs.NewStore(db.Pool)
	dispatcher := jobs.NewDispatcher(jobStore)
	if err := dispatcher.Recover(context.Background()); err != nil {
		logger.Error("job recovery failed", "error", err)
		os.Exit(1)
	}
	workerContext, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	accountGuard := accounts.NewGuard(cfg)
	quotaStore := accounts.NewQuotaStore(db.Pool)
	runner := indexing.NewRunner(cfg, indexing.NewStore(db.Pool), jobStore, accountGuard, quotaStore, jobStore)
	go runDispatcher(workerContext, logger, dispatcher, runner)
	retentionService := retention.New(cfg, retention.NewStore(db.Pool), accountGuard, quotaStore)
	watchlistStore := watchlist.NewStore(db.Pool)
	go runWatchlist(workerContext, logger, watchlistStore, retentionService)
	handler := httpapi.NewHandlerWithServices("dev", db.Ready, jobStore, jobStore, articles.NewStore(db.Pool))
	handler = httpapi.WithBodyRetrieval(handler, retrieval.New(cfg, articles.NewStore(db.Pool), accountGuard, quotaStore))
	handler = httpapi.WithProviderStatus(handler, providers.New(cfg, quotaStore, accountGuard))
	qualificationService := qualification.New(cfg, accountGuard, quotaStore)
	qualificationService.History = qualification.NewStore(db.Pool)
	handler = httpapi.WithProviderPreflight(handler, qualificationService)
	handler = httpapi.WithStorageBrowser(handler, articles.NewStore(db.Pool), indexing.NewStore(db.Pool))
	handler = httpapi.WithRetention(handler, retentionService)
	handler = httpapi.WithWatchlist(handler, watchlistStore)
	handler = httpapi.WithChronologicalBrowser(handler, articles.NewStore(db.Pool))
	handler = httpapi.WithTimeline(handler, timeline.NewStore(db.Pool, jobStore))
	handler = httpapi.WithMetrics(handler, func() httpapi.MetricSnapshot {
		stat := db.Pool.Stat()
		return httpapi.MetricSnapshot{DBAcquiredConns: stat.AcquiredConns(), DBIdleConns: stat.IdleConns(), DBAcquireCount: stat.AcquireCount()}
	})
	if webRoot := os.Getenv("USENET_LOCATOR_WEB_ROOT"); webRoot != "" {
		if _, err := os.Stat(webRoot); err != nil {
			logger.Error("web root is unavailable", "path", webRoot, "error", err)
			os.Exit(1)
		}
		handler = httpapi.WithStaticFiles(handler, os.DirFS(webRoot))
	} else if _, err := os.Stat("/web"); err == nil {
		handler = httpapi.WithStaticFiles(handler, os.DirFS("/web"))
	}
	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("HTTP server starting", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
}

func runWatchlist(ctx context.Context, logger *slog.Logger, store watchlist.Store, prober retention.Service) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := watchlist.RunDue(ctx, store, prober, time.Now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("watchlist check failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runDispatcher(ctx context.Context, logger *slog.Logger, dispatcher jobs.Dispatcher, runner indexing.Runner) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		claimed, err := dispatcher.RunOnce(ctx, runner.Run)
		if err != nil {
			logger.Error("index job interrupted", "error", err)
		}
		if claimed && err == nil {
			logger.Info("index job completed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
