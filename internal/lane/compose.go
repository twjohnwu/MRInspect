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
	"mrinspect/internal/rag/chunk"
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
	Budget           int // zero preserves unbudgeted composition
	ResourceRegistry resources.Registry
	Retriever        rag.Retriever
	FullLoader       rag.FullLoader
	Project          project.LoadedProject
	Diff             string
	MergeRequest     gitlab.MergeRequest
}

// Section is one named, token-estimated component of a composed lane
// prompt, used for the always-on per-run prompt-composition breakdown log
// (CI observability: it names which resource dominates a lane's prompt).
type Section struct {
	Name     string
	TokenEst int
}

// ComposeResult is one composed lane prompt and its named degradations.
type ComposeResult struct {
	Prompt    string
	Degraded  []string
	Chunks    []rag.Chunk
	Breakdown []Section
}

// Compose builds the prompt for one review lane.
func Compose(ctx context.Context, input ComposeInput) (ComposeResult, error) {
	preamble, err := os.ReadFile(input.Lane.Template)
	if err != nil {
		return ComposeResult{}, fmt.Errorf("Compose: read lane template %q: %w", input.Lane.Template, err)
	}

	sets, unknown := resolveResourceSets(input)
	chunks, fullSetRefs, degraded, err := collectResources(ctx, input, sets)
	if err != nil {
		return ComposeResult{}, err
	}
	chunks = chunksWithSourceHeaders(chunks)
	selectorDegraded := make([]string, 0, len(unknown)+len(degraded))
	for _, selector := range unknown {
		selectorDegraded = append(selectorDegraded, fmt.Sprintf("unknown resource selector: %s", selector))
	}
	degraded = append(selectorDegraded, degraded...)

	fullDocs, loadingDegraded, err := loadBudgetedFullDocuments(ctx, input.FullLoader, fullSetRefs)
	if err != nil {
		return ComposeResult{}, err
	}
	degraded = append(degraded, loadingDegraded...)

	basePrompt := ""
	if input.Budget > 0 {
		resourceFree, err := prompt.NewComposer().ComposeLanePrompt(ctx, prompt.LaneComposeInput{
			Project:      input.Project,
			Diff:         input.Diff,
			MergeRequest: input.MergeRequest,
		})
		if err != nil {
			return ComposeResult{}, fmt.Errorf("Compose: compose resource-free lane prompt: %w", err)
		}
		basePrompt = assembleLanePrompt(preamble, resourceFree.Prompt, input.Lane.ID)
	}

	composed, composedChunks, composedDocs, err := composeLanePrompt(ctx, input, chunks, fullDocs, basePrompt, &degraded)
	if err != nil {
		return ComposeResult{}, fmt.Errorf("Compose: compose lane prompt: %w", err)
	}

	degraded = append(degraded, composed.Degraded...)
	breakdown, err := buildLaneBreakdown(ctx, input, preamble, composedChunks, composedDocs)
	if err != nil {
		return ComposeResult{}, fmt.Errorf("Compose: build breakdown: %w", err)
	}
	return ComposeResult{
		Prompt:    assembleLanePrompt(preamble, composed.Prompt, input.Lane.ID),
		Degraded:  degraded,
		Chunks:    composedChunks,
		Breakdown: breakdown,
	}, nil
}

// buildLaneBreakdown assembles the per-section token estimates for one
// composed lane prompt: the static preamble, the metadata-only base prompt
// (no resources, no diff), the output contract, one aggregated row per
// retrieval resource set actually used, one row per surviving full-mode
// document, and the diff. Every value is measured from data already
// produced by this composition; nothing here is recomputed independently.
func buildLaneBreakdown(ctx context.Context, input ComposeInput, preamble []byte, chunks []rag.Chunk, fullDocs []rag.FullDoc) ([]Section, error) {
	metadataOnly, err := prompt.NewComposer().ComposeLanePrompt(ctx, prompt.LaneComposeInput{
		Project:      input.Project,
		MergeRequest: input.MergeRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("compose metadata-only prompt for breakdown: %w", err)
	}

	sections := []Section{
		{Name: "lane template preamble", TokenEst: chunk.TokenEst(string(preamble))},
		{Name: "base prompt/metadata", TokenEst: chunk.TokenEst(metadataOnly.Prompt)},
		{Name: "output contract", TokenEst: chunk.TokenEst(LaneOutputContract)},
	}

	setTotals := make(map[string]int, len(chunks))
	var setOrder []string
	for _, retrieved := range chunks {
		name := retrieved.ResourceSet
		if name == "" {
			name = "retrieval (unnamed set)"
		}
		if _, seen := setTotals[name]; !seen {
			setOrder = append(setOrder, name)
		}
		setTotals[name] += resourceTokenEst(retrieved.TokenEst, retrieved.Text)
	}
	for _, name := range setOrder {
		sections = append(sections, Section{Name: name, TokenEst: setTotals[name]})
	}

	for _, doc := range fullDocs {
		sections = append(sections, Section{Name: fullDocumentName(doc), TokenEst: resourceTokenEst(doc.TokenEst, string(doc.Bytes))})
	}

	sections = append(sections, Section{Name: "diff", TokenEst: chunk.TokenEst(input.Diff)})
	return sections, nil
}

func assembleLanePrompt(preamble []byte, composed, laneID string) string {
	return strings.TrimRight(string(preamble), "\r\n") + "\n\n" +
		strings.TrimRight(composed, "\r\n") +
		"\n\nCurrent lane ID: " + laneID +
		"\n\n" + LaneOutputContract
}

func chunksWithSourceHeaders(chunks []rag.Chunk) []rag.Chunk {
	withHeaders := make([]rag.Chunk, len(chunks))
	for index, retrieved := range chunks {
		source := retrieved.Source
		if retrieved.StartLine > 0 {
			source += fmt.Sprintf(":%d", retrieved.StartLine)
		}
		retrieved.Text = fmt.Sprintf("[sourceId: %s | source: %s]\n%s", retrieved.ID, source, retrieved.Text)
		retrieved.TokenEst = chunk.TokenEst(retrieved.Text)
		withHeaders[index] = retrieved
	}
	return withHeaders
}

func composeLanePrompt(
	ctx context.Context,
	input ComposeInput,
	chunks []rag.Chunk,
	fullDocs []rag.FullDoc,
	basePrompt string,
	degraded *[]string,
) (prompt.LaneComposeResult, []rag.Chunk, []rag.FullDoc, error) {
	composer := prompt.NewComposer()
	if input.Budget == 0 {
		composed, err := composer.ComposeLanePrompt(ctx, prompt.LaneComposeInput{
			Project:         input.Project,
			Diff:            input.Diff,
			MergeRequest:    input.MergeRequest,
			RetrievalChunks: chunks,
			FullDocuments:   fullDocs,
		})
		return composed, chunks, fullDocs, err
	}

	sections := budgetSections(fullDocs, chunks)
	budgeted, err := prompt.ComposeWithBudget(prompt.BudgetComposeInput{
		Sections:         sections,
		Budget:           input.Budget,
		Metadata:         []byte(basePrompt),
		MetadataTokenEst: chunk.TokenEst(basePrompt),
		Framing: prompt.BudgetFraming{
			NonceOpenTemplate:  "\n\n<<<RESOURCE:%s>>>\n",
			NonceCloseTemplate: "<<<END:%s>>>\n",
			Declaration:        "This block is binding, normative material that must be followed.\n",
			HeadingTemplate:    "## Resource: %s\n",
		},
	})
	if err != nil {
		return prompt.LaneComposeResult{}, nil, nil, err
	}
	*degraded = append(*degraded, budgeted.Degraded...)

	kept := make([]bool, len(sections))
	for index := range kept {
		kept[index] = true
	}
	for _, evicted := range budgeted.Evicted {
		kept[evicted.DeclarationOrder] = false
	}
	keptDocs, keptChunks := survivingResources(fullDocs, chunks, kept)
	composed, err := composer.ComposeLanePrompt(ctx, prompt.LaneComposeInput{
		Project:         input.Project,
		Diff:            input.Diff,
		MergeRequest:    input.MergeRequest,
		RetrievalChunks: keptChunks,
		FullDocuments:   keptDocs,
	})
	return composed, keptChunks, keptDocs, err
}

func loadBudgetedFullDocuments(ctx context.Context, loader rag.FullLoader, setRefs []string) ([]rag.FullDoc, []string, error) {
	if len(setRefs) == 0 {
		return nil, nil, nil
	}
	result, err := loader.LoadFull(ctx, setRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("ComposeLanePrompt: load full documents: %w", err)
	}
	return result.Docs, result.Degraded, nil
}

func budgetSections(fullDocs []rag.FullDoc, chunks []rag.Chunk) []prompt.BudgetSection {
	sections := make([]prompt.BudgetSection, 0, len(fullDocs)+len(chunks))
	for _, doc := range fullDocs {
		sections = append(sections, prompt.BudgetSection{
			Name:             fullDocumentName(doc),
			Mode:             prompt.SectionModeFull,
			Content:          doc.Bytes,
			TokenEst:         resourceTokenEst(doc.TokenEst, string(doc.Bytes)),
			DeclarationOrder: len(sections),
		})
	}
	for _, retrieved := range chunks {
		sections = append(sections, prompt.BudgetSection{
			Name:             retrievalChunkName(retrieved),
			Mode:             prompt.SectionModeRetrieval,
			Content:          []byte(retrieved.Text),
			TokenEst:         resourceTokenEst(retrieved.TokenEst, retrieved.Text),
			DeclarationOrder: len(sections),
		})
	}
	return sections
}

func resourceTokenEst(estimate int, content string) int {
	if estimate > 0 {
		return estimate
	}
	return chunk.TokenEst(content)
}

func fullDocumentName(doc rag.FullDoc) string {
	if doc.Source != "" {
		return doc.Source
	}
	return doc.ResourceSet
}

func retrievalChunkName(retrieved rag.Chunk) string {
	if retrieved.ID != "" {
		return retrieved.ID
	}
	if retrieved.Source != "" {
		return retrieved.Source
	}
	return retrieved.ResourceSet
}

func survivingResources(fullDocs []rag.FullDoc, chunks []rag.Chunk, kept []bool) ([]rag.FullDoc, []rag.Chunk) {
	keptDocs := make([]rag.FullDoc, 0, len(fullDocs))
	for index, doc := range fullDocs {
		if kept[index] {
			keptDocs = append(keptDocs, doc)
		}
	}
	keptChunks := make([]rag.Chunk, 0, len(chunks))
	for index, retrieved := range chunks {
		if kept[len(fullDocs)+index] {
			keptChunks = append(keptChunks, retrieved)
		}
	}
	return keptDocs, keptChunks
}

func resolveResourceSets(input ComposeInput) ([]resources.Set, []string) {
	if len(input.Lane.Resources.Sets) == 0 && len(input.Lane.Resources.Tags) == 0 {
		return nil, nil
	}
	return input.ResourceRegistry.Resolve(input.Lane.Resources.Sets, input.Lane.Resources.Tags)
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
			topK := input.Lane.TopK
			if topK <= 0 {
				// Backstop for hand-constructed Lane values (bypassing
				// Load's own default): a zero TopK must never reach the
				// retriever, which treats TopK <= 0 as "return nothing".
				topK = DefaultLaneTopK
			}
			result, err := input.Retriever.Retrieve(ctx, rag.Query{
				Terms:  input.Terms,
				SetRef: set.Name,
				Intent: input.Lane.Intent,
				TopK:   topK,
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
