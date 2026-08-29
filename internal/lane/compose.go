package lane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"mrinspect/internal/gitlab"
	"mrinspect/internal/project"
	"mrinspect/internal/prompt"
	"mrinspect/internal/rag"
	"mrinspect/internal/rag/resources"
)

var (
	errRetrieverRequired  = errors.New("Compose: retrieval resource sets require a Retriever")
	errFullLoaderRequired = errors.New("Compose: full resource sets require a FullLoader")
)

// ComposeInput contains the lane-specific and shared inputs needed to build one prompt.
type ComposeInput struct {
	Lane             Lane
	Terms            []string
	ResourceRegistry resources.Registry
	Retriever        rag.Retriever
	FullLoader       rag.FullLoader
	Project          project.LoadedProject
	Diff             string
	MergeRequest     gitlab.MergeRequest
}

// ComposeResult is one composed lane prompt and its named degradations.
type ComposeResult struct {
	Prompt   string
	Degraded []string
	Chunks   []rag.Chunk
}

// Compose builds the prompt for one review lane.
func Compose(ctx context.Context, input ComposeInput) (ComposeResult, error) {
	preamble, err := os.ReadFile(input.Lane.Template)
	if err != nil {
		return ComposeResult{}, fmt.Errorf("Compose: read lane template %q: %w", input.Lane.Template, err)
	}

	sets := resolveResourceSets(input)
	chunks, fullSetRefs, degraded, err := collectResources(ctx, input, sets)
	if err != nil {
		return ComposeResult{}, err
	}

	fullLoader := input.FullLoader
	if len(fullSetRefs) == 0 {
		fullLoader = nil
	}
	composed, err := prompt.NewComposer().ComposeLanePrompt(ctx, prompt.LaneComposeInput{
		Project:         input.Project,
		Diff:            input.Diff,
		MergeRequest:    input.MergeRequest,
		RetrievalChunks: chunks,
		FullSetRefs:     fullSetRefs,
		FullLoader:      fullLoader,
	})
	if err != nil {
		return ComposeResult{}, fmt.Errorf("Compose: compose lane prompt: %w", err)
	}

	degraded = append(degraded, composed.Degraded...)
	return ComposeResult{
		Prompt: strings.TrimRight(string(preamble), "\r\n") + "\n\n" +
			strings.TrimRight(composed.Prompt, "\r\n") +
			"\n\nCurrent lane ID: " + input.Lane.ID +
			"\n\n" + LaneOutputContract,
		Degraded: degraded,
		Chunks:   chunks,
	}, nil
}

func resolveResourceSets(input ComposeInput) []resources.Set {
	if len(input.Lane.Resources.Sets) == 0 && len(input.Lane.Resources.Tags) == 0 {
		return nil
	}
	sets, _ := input.ResourceRegistry.Resolve(input.Lane.Resources.Sets, input.Lane.Resources.Tags)
	return sets
}

func collectResources(ctx context.Context, input ComposeInput, sets []resources.Set) ([]rag.Chunk, []string, []string, error) {
	var chunks []rag.Chunk
	var fullSetRefs []string
	var degraded []string

	for _, set := range sets {
		switch set.Mode {
		case resources.ModeRetrieval:
			if input.Retriever == nil {
				return nil, nil, nil, errRetrieverRequired
			}
			result, err := input.Retriever.Retrieve(ctx, rag.Query{
				Terms:  input.Terms,
				SetRef: set.Name,
				Intent: input.Lane.Intent,
				TopK:   input.Lane.TopK,
			})
			if err != nil {
				return nil, nil, nil, fmt.Errorf("Compose: retrieve resource set %q: %w", set.Name, err)
			}
			chunks = append(chunks, result.Chunks...)
			degraded = append(degraded, result.Degraded...)
		case resources.ModeFull:
			if input.FullLoader == nil {
				return nil, nil, nil, errFullLoaderRequired
			}
			fullSetRefs = append(fullSetRefs, set.Name)
		default:
			return nil, nil, nil, fmt.Errorf("Compose: resource set %q has unsupported mode %q", set.Name, set.Mode)
		}
	}

	return chunks, fullSetRefs, degraded, nil
}
