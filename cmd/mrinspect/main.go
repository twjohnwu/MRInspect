package main

import (
	"context"
	"errors"
	"flag"
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
	"mrinspect/internal/ragcmd"
	"mrinspect/internal/reviewer"
	"mrinspect/internal/validator"
)

func main() {
	ctx := context.Background()
	path, args := ragcmd.Dispatch(os.Args[1:])
	if path == ragcmd.PathIndex {
		cfg, err := config.LoadForIndex()
		if err != nil {
			slog.Error("index configuration error", "error", err)
			os.Exit(1)
		}
		opts, err := ragcmd.ParseOptions(args, cfg.Service.Name, os.Stdout)
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				os.Exit(0)
			}
			slog.Error("index arguments error", "error", err)
			os.Exit(1)
		}
		exitCode, _, err := ragcmd.RunIndex(ctx, opts)
		if err != nil {
			slog.Error("index error", "error", err)
		}
		os.Exit(exitCode)
	}

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
