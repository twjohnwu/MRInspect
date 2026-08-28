package prompt

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"mrinspect/internal/rag/chunk"
)

// SectionMode identifies how a resource section is injected.
type SectionMode string

const (
	SectionModeRetrieval SectionMode = "retrieval"
	SectionModeFull      SectionMode = "full"
)

// BudgetSection is an already-assembled resource section. Content is retained
// verbatim when the section is selected; TokenEst is supplied by its producer.
type BudgetSection struct {
	Name             string
	Mode             SectionMode
	Content          []byte
	TokenEst         int
	DeclarationOrder int
}

// BudgetFraming declares the fixed-shape text around resource sections.
// NonceTemplate is formatted with a 32-hex-character per-composition nonce.
type BudgetFraming struct {
	NonceOpenTemplate  string
	NonceCloseTemplate string
	Declaration        string
	HeadingTemplate    string
	JSONFraming        string
}

// BudgetComposeInput supplies the immutable pieces to one budgeted prompt
// composition. OnSectionExcluded observes removal from the composition set;
// it is deliberately separate from ComposeResult.Evicted reporting.
type BudgetComposeInput struct {
	Budget            int
	Diff              []byte
	DiffTokenEst      int
	Metadata          []byte
	MetadataTokenEst  int
	Sections          []BudgetSection
	Framing           BudgetFraming
	OnSectionExcluded func(BudgetSection)
}

// EvictedSection records one whole section removed during composition.
type EvictedSection struct {
	Name             string
	Mode             SectionMode
	DeclarationOrder int
	TokenEst         int
}

// ComposeResult is the observable result of a budgeted composition.
type ComposeResult struct {
	Prompt              string
	Evicted             []EvictedSection
	Degraded            []string
	NormativeEvicted    bool
	FramingOverhead     int
	JSONFramingOverhead int
}

// PromptBudgetForModel returns floor(modelLimit * MRI_PROMPT_BUDGET_FACTOR),
// using 0.8 when that environment variable is unset. limits is the caller's
// per-model limit table; an unknown model is a configuration error. It returns
// -1 on configuration errors so a caller that ignores the error cannot mistake
// zero for a valid budget and silently evict every section.
func PromptBudgetForModel(model string, limits map[string]int) (int, error) {
	limit, ok := limits[model]
	if !ok {
		return -1, fmt.Errorf("unknown prompt-budget model %q", model)
	}

	factor := 0.8
	if raw, ok := os.LookupEnv("MRI_PROMPT_BUDGET_FACTOR"); ok && raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return -1, fmt.Errorf("invalid MRI_PROMPT_BUDGET_FACTOR %q", raw)
		}
		factor = parsed
	}
	return int(math.Floor(float64(limit) * factor)), nil
}

// ComposeWithBudget selects whole sections and assembles a prompt under input.Budget.
func ComposeWithBudget(input BudgetComposeInput) (ComposeResult, error) {
	jsonOverhead := chunk.TokenEst(input.Framing.JSONFraming)
	framingOverhead := 0
	for _, section := range input.Sections {
		framingOverhead += sectionFramingTokenEst(input.Framing, section.Name)
	}

	result := ComposeResult{
		FramingOverhead:     framingOverhead,
		JSONFramingOverhead: jsonOverhead,
	}
	kept := make([]bool, len(input.Sections))
	for i := range kept {
		kept[i] = true
	}
	cost := input.DiffTokenEst + input.MetadataTokenEst + jsonOverhead + framingOverhead
	for _, section := range input.Sections {
		cost += section.TokenEst
	}

	for _, mode := range []SectionMode{SectionModeRetrieval, SectionModeFull} {
		for cost > input.Budget {
			index := tailSectionIndex(input.Sections, kept, mode)
			if index < 0 {
				break
			}
			section := input.Sections[index]
			kept[index] = false // The section leaves the composition set here.
			if input.OnSectionExcluded != nil {
				input.OnSectionExcluded(section)
			}
			result.Evicted = append(result.Evicted, EvictedSection{
				Name:             section.Name,
				Mode:             section.Mode,
				DeclarationOrder: section.DeclarationOrder,
				TokenEst:         section.TokenEst,
			})
			result.Degraded = append(result.Degraded, fmt.Sprintf("evicted section %q", section.Name))
			cost -= section.TokenEst + sectionFramingTokenEst(input.Framing, section.Name)
		}
	}

	if cost > input.Budget {
		return result, fmt.Errorf("prompt exceeds budget after section eviction: diff TokenEst=%d, framing overhead=%d, budget=%d", input.DiffTokenEst, result.FramingOverhead+result.JSONFramingOverhead, input.Budget)
	}
	for _, section := range result.Evicted {
		if section.Mode == SectionModeFull {
			result.NormativeEvicted = true
			break
		}
	}
	if result.NormativeEvicted && os.Getenv("MRI_RAG_ON_NORMATIVE_EVICTION") == "fail" {
		return result, fmt.Errorf("prompt composition rejected: normative section evicted")
	}

	nonce, err := compositionNonce(input.Sections, kept)
	if err != nil {
		return ComposeResult{Evicted: result.Evicted, Degraded: result.Degraded, NormativeEvicted: result.NormativeEvicted, FramingOverhead: result.FramingOverhead, JSONFramingOverhead: result.JSONFramingOverhead}, err
	}
	var prompt strings.Builder
	prompt.WriteString(input.Framing.JSONFraming)
	prompt.Write(input.Metadata)
	prompt.Write(input.Diff)
	for i, section := range input.Sections {
		if !kept[i] {
			continue
		}
		prompt.WriteString(input.Framing.Declaration)
		prompt.WriteString(fmt.Sprintf(input.Framing.HeadingTemplate, section.Name))
		prompt.WriteString(fmt.Sprintf(input.Framing.NonceOpenTemplate, nonce))
		prompt.Write(section.Content)
		prompt.WriteString(fmt.Sprintf(input.Framing.NonceCloseTemplate, nonce))
	}
	result.Prompt = prompt.String()
	return result, nil
}

const nonceHexLength = 32

func sectionFramingTokenEst(framing BudgetFraming, name string) int {
	placeholder := strings.Repeat("a", nonceHexLength)
	text := fmt.Sprintf(framing.NonceOpenTemplate, placeholder) +
		fmt.Sprintf(framing.NonceCloseTemplate, placeholder) +
		framing.Declaration +
		fmt.Sprintf(framing.HeadingTemplate, name)
	return chunk.TokenEst(text)
}

// tailSectionIndex uses only slice iteration so the eviction path has no map
// iteration and always removes the greatest declaration order first.
func tailSectionIndex(sections []BudgetSection, kept []bool, mode SectionMode) int {
	index := -1
	for i, section := range sections {
		if kept[i] && section.Mode == mode && (index < 0 || section.DeclarationOrder > sections[index].DeclarationOrder) {
			index = i
		}
	}
	return index
}

func compositionNonce(sections []BudgetSection, kept []bool) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		bytes := make([]byte, nonceHexLength/2)
		if _, err := rand.Read(bytes); err != nil {
			return "", fmt.Errorf("generate composition nonce: %w", err)
		}
		nonce := hex.EncodeToString(bytes)
		collision := false
		for i, section := range sections {
			if kept[i] && strings.Contains(string(section.Content), nonce) {
				collision = true
				break
			}
		}
		if !collision {
			return nonce, nil
		}
	}
	return "", fmt.Errorf("generate composition nonce: collision after 3 attempts")
}
