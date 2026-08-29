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
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
	"mrinspect/internal/ragcmd"
	"mrinspect/internal/ragwire"
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
	rag.RegisterBuiltinSources(rag.BuiltinSourcesConfig{GitLab: rag.GitLabSourceConfig{
		APIBase: cfg.GitLabAPIBase, Token: cfg.GitLabToken, ProjectID: v.GetProjectID(),
		PackageName: "rag-index", ArtifactRef: v.GetTargetBranch(), ArtifactJob: "rag-index",
		StoreName: "mrinspect-rag.sqlite",
	}})
	repoRoot := "."
	resourceRegistry, err := resources.Load(repoRoot, "")
	if err != nil {
		log.Warn("failed to load RAG resource sets", "error", err)
	}
	gitlabClient := gitlab.NewClient(cfg, log)

	localFetcher := diff.NewLocalDiffFetcher(log)
	apiFetcher := diff.NewAPIDiffFetcher(gitlabClient, v.GetProjectID(), v.GetMRIID(), log)

	var diffFetcher interfaces.IDiffFetcher
	if cfg.CrossRepo.Enabled {
		diffFetcher = apiFetcher
	} else {
		diffFetcher = diff.NewFallbackDiffFetcher(localFetcher, apiFetcher, log)
	}

	modelLimits, err := prompt.ModelLimitsFromEnv()
	if err != nil {
		log.Error("model limits configuration error", "error", err)
		os.Exit(1)
	}

	projectLoader := project.NewLoader(cfg.Projects)
	promptComposer := prompt.NewComposer()
	errHandler := mrerrors.NewHandler(cfg, log)

	r := reviewer.New(cfg, gitlabClient, aiProvider, diffFetcher,
		projectLoader, promptComposer, v, errHandler, log)
	productionRAG := ragwire.NewProductionReviewDependencies(cfg, ragwire.ReviewPathConfig{
		ResolverConfig: rag.DefaultResolverConfig(),
		ResourceSets:   resourceRegistry.Sets,
		Composer:       promptComposer,
	})
	defer func() {
		if err := productionRAG.Retriever.Close(); err != nil {
			log.Warn("failed to clean up resolved RAG store", "error", err)
		}
	}()
	r.SetRAGReviewPath(productionRAG.ReviewPath)
	r.SetMultiLaneReviewPath(reviewer.MultiLaneReviewPath{
		RepoRoot:         repoRoot,
		ResourceRegistry: resourceRegistry,
		Retriever:        productionRAG.Retriever,
		FullLoader:       productionRAG.FullLoader,
		ModelLimits:      modelLimits,
	})

	r.Run(ctx)
}
