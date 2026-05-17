package main

import (
	"context"
	"log/slog"
	"os"

	"mrinspect/internal/ai"
	"mrinspect/internal/config"
	"mrinspect/internal/diff"
	mrerrors "mrinspect/internal/errors"
	"mrinspect/internal/gitlab"
	"mrinspect/internal/interfaces"
	"mrinspect/internal/logger"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/reviewer"
	"mrinspect/internal/validator"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	log := logger.New(logLevel, cfg.MetricsFile)

	aiProvider, err := ai.NewProvider(cfg, log)
	if err != nil {
		log.Error("failed to initialize AI provider", "provider", cfg.AIProvider, "error", err)
		os.Exit(1)
	}

	v := validator.New(cfg)
	gitlabClient := gitlab.NewClient(cfg, log)

	localFetcher := diff.NewLocalDiffFetcher(log)
	apiFetcher := diff.NewAPIDiffFetcher(gitlabClient, v.GetProjectID(), v.GetMRIID(), log)

	var diffFetcher interfaces.IDiffFetcher
	if cfg.CrossRepo.Enabled {
		diffFetcher = apiFetcher
	} else {
		diffFetcher = diff.NewFallbackDiffFetcher(localFetcher, apiFetcher, log)
	}

	projectLoader := project.NewLoader(cfg.Projects)
	promptComposer := prompt.NewComposer()
	errHandler := mrerrors.NewHandler(cfg, log)

	r := reviewer.New(cfg, gitlabClient, aiProvider, diffFetcher,
		projectLoader, promptComposer, v, errHandler, log)

	r.Run(ctx)
}
