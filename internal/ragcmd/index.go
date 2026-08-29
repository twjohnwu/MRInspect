// Package ragcmd contains command-level RAG operations.
package ragcmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"mrinspect/internal/rag/resources"
	"mrinspect/internal/rag/sqlite"
)

// Path identifies the top-level command path selected by Dispatch.
type Path uint8

const (
	// PathReview preserves the existing bare mrinspect review flow (REQ-05 / S-18).
	PathReview Path = iota
	// PathIndex selects the RAG store indexing command (REQ-05).
	PathIndex
)

// Dispatch classifies command-line arguments and returns the selected path plus
// arguments remaining for that path (REQ-05 / S-18).
func Dispatch(args []string) (path Path, rest []string) {
	if len(args) > 0 && args[0] == "index" {
		return PathIndex, args[1:]
	}
	return PathReview, args
}

// ResourceLoader resolves the resource sets available to an index invocation.
// It is injectable so command policy can be tested independently of a registry.
type ResourceLoader interface {
	Load(context.Context) ([]resources.Set, error)
}

// Indexer is the backend seam used by RunIndex. Implementations report whether
// indexing is supported before attempting a store write.
type Indexer interface {
	SupportsIndexing() bool
	Index(context.Context, string, []resources.Set) (IndexStats, error)
}

// Options configures one index invocation (REQ-05 / S-20 / S-21).
type Options struct {
	OutputPath string
	DryRun     bool
	Output     io.Writer
	Loader     ResourceLoader
	Indexer    Indexer
}

// IndexStats is the command-level result reported to the caller and CLI.
// Message names the outcome so callers can present distinct exit-code causes.
type IndexStats struct {
	ResourceSets int
	FilesIndexed int
	FilesFailed  int
	Message      string
}

// RunIndex loads resources and indexes them, returning an observable exit code
// instead of exiting directly (REQ-05 / S-20 / S-21).
func RunIndex(ctx context.Context, opts Options) (exitCode int, stats IndexStats, err error) {
	if opts.Loader == nil {
		return indexFailure(opts, stats, "resource loader is not configured", nil)
	}
	if opts.Indexer == nil {
		return indexFailure(opts, stats, "index backend is not configured", nil)
	}

	sets, err := opts.Loader.Load(ctx)
	if err != nil {
		return indexFailure(opts, stats, "resource loading failed", err)
	}
	stats.ResourceSets = len(sets)
	if len(sets) == 0 {
		stats.Message = "no resource sets resolved"
		printStats(opts.Output, stats)
		return 2, stats, nil
	}
	if !opts.Indexer.SupportsIndexing() {
		stats.Message = "backend does not support indexing"
		printStats(opts.Output, stats)
		return 5, stats, nil
	}
	if opts.DryRun {
		stats.Message = "dry-run statistics: indexing was not performed"
		printStats(opts.Output, stats)
		return 0, stats, nil
	}

	stats, err = opts.Indexer.Index(ctx, opts.OutputPath, sets)
	stats.ResourceSets = len(sets)
	if err != nil {
		return indexFailure(opts, stats, "indexing failed", err)
	}
	if stats.FilesFailed > 0 {
		stats.Message = "some files failed during indexing"
		printStats(opts.Output, stats)
		return 3, stats, nil
	}

	stats.Message = "indexed successfully"
	printStats(opts.Output, stats)
	return 0, stats, nil
}

// ParseOptions converts index-subcommand arguments into the command seams.
// serviceName selects the optional per-system resource overlay.
func ParseOptions(args []string, serviceName string, output io.Writer) (Options, error) {
	flags := flag.NewFlagSet("index", flag.ContinueOnError)
	flags.SetOutput(output)
	outputPath := flags.String("out", ".rag/mrinspect-rag.sqlite", "path for the SQLite resource store")
	dryRun := flags.Bool("dry-run", false, "report indexing statistics without writing a store")
	help := flags.Bool("help", false, "show index command help")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: mrinspect index [--out PATH] [--dry-run]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return Options{}, err
	}
	if *help {
		flags.Usage()
		return Options{}, flag.ErrHelp
	}
	if flags.NArg() != 0 {
		return Options{}, fmt.Errorf("index: unexpected arguments: %v", flags.Args())
	}

	return Options{
		OutputPath: *outputPath,
		DryRun:     *dryRun,
		Output:     output,
		Loader:     resourceSetLoader{repoRoot: ".", system: serviceName},
		Indexer:    sqliteIndexer{backend: os.Getenv("MRI_RAG_BACKEND")},
	}, nil
}

func indexFailure(opts Options, stats IndexStats, cause string, err error) (int, IndexStats, error) {
	stats.Message = cause
	printStats(opts.Output, stats)
	if err != nil {
		return 1, stats, fmt.Errorf("%s: %w", cause, err)
	}
	return 1, stats, fmt.Errorf("%s", cause)
}

func printStats(output io.Writer, stats IndexStats) {
	if output == nil {
		return
	}
	fmt.Fprintf(output, "%s: resource sets=%d files indexed=%d files failed=%d\n",
		stats.Message, stats.ResourceSets, stats.FilesIndexed, stats.FilesFailed)
}

type resourceSetLoader struct {
	repoRoot string
	system   string
}

func (l resourceSetLoader) Load(context.Context) ([]resources.Set, error) {
	registry, err := resources.Load(l.repoRoot, l.system)
	if err != nil {
		return nil, err
	}
	return registry.Sets, nil
}

type sqliteIndexer struct {
	backend string
}

func (i sqliteIndexer) SupportsIndexing() bool {
	return i.backend == "" || i.backend == "sqlite"
}

func (sqliteIndexer) Index(ctx context.Context, output string, sets []resources.Set) (IndexStats, error) {
	stats, err := sqlite.Index(ctx, sqlite.IndexOptions{OutputPath: output, Sets: sets})
	return IndexStats{FilesFailed: len(stats.Failures)}, err
}
