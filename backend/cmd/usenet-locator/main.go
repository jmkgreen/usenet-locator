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

	"github.com/james/usenet-locator/backend/internal/accounts"
	"github.com/james/usenet-locator/backend/internal/articles"
	"github.com/james/usenet-locator/backend/internal/config"
	"github.com/james/usenet-locator/backend/internal/database"
	"github.com/james/usenet-locator/backend/internal/httpapi"
	"github.com/james/usenet-locator/backend/internal/indexing"
	"github.com/james/usenet-locator/backend/internal/jobs"
	"github.com/james/usenet-locator/backend/internal/providers"
	"github.com/james/usenet-locator/backend/internal/qualification"
	"github.com/james/usenet-locator/backend/internal/retrieval"
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
	databaseURL := os.Getenv(cfg.Database.URLFromEnv)
	if databaseURL == "" {
		logger.Error("database URL secret is unavailable", "environment", cfg.Database.URLFromEnv)
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
	runner := indexing.NewRunner(cfg, indexing.NewStore(db.Pool), jobStore, accountGuard, quotaStore)
	go runDispatcher(workerContext, logger, dispatcher, runner)
	handler := httpapi.NewHandlerWithServices("dev", db.Ready, jobStore, jobStore, articles.NewStore(db.Pool))
	handler = httpapi.WithBodyRetrieval(handler, retrieval.New(cfg, articles.NewStore(db.Pool), accountGuard, quotaStore))
	handler = httpapi.WithProviderStatus(handler, providers.New(cfg, quotaStore, accountGuard))
	qualificationService := qualification.New(cfg, accountGuard, quotaStore)
	qualificationService.History = qualification.NewStore(db.Pool)
	handler = httpapi.WithProviderPreflight(handler, qualificationService)
	handler = httpapi.WithStorageBrowser(handler, articles.NewStore(db.Pool), indexing.NewStore(db.Pool))
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
