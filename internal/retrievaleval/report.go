package retrievaleval

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
)

const reportPool = "off=TopK+1 on=4xTopK shuffle=4xTopK×20"

const (
	metricOrigRecall = iota
	metricOrigMRR
	metricParaRecall
	metricParaMRR
	metricDistractors
	metricCount
)

var (
	resourcesSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	retrieveMsPattern      = regexp.MustCompile(`^off_mean=\d+ on_mean=(\d+|-) \(n=\d+\)$`)
)

type Cell struct {
	Value    float64
	Degraded string
	Integer  bool
}

type Triplet struct {
	Off  Cell
	Shuf Cell
	On   Cell
}

type RecallByK struct {
	Orig Triplet
	Para Triplet
}

func (t Triplet) cells() [3]Cell {
	return [3]Cell{t.Off, t.Shuf, t.On}
}

type Row struct {
	System     string
	Fixture    string
	Lane       string
	Set        string
	K          int
	Metrics    [metricCount]Triplet
	RecallByK  map[int]RecallByK
	Categories map[string]Triplet
	// OffMs and OnMs are the OFF/ON Retrieve durations for this triple, in
	// milliseconds. Populated by run.go, rendered into the retrieve_ms
	// header line (REQ-04); GREEN fills these in.
	OffMs int64
	OnMs  int64
}

type Header struct {
	BuiltAt      string
	ResourcesSHA string
	EmbedModel   string
	Pool         string
	GeneratedAt  string
	// RetrieveMs is the rendered "retrieve_ms: off_mean=… on_mean=… (n=…)"
	// header line (REQ-04).
	RetrieveMs string
}

// Render writes the retrieval-quality report.
func Render(w io.Writer, h Header, rows []Row, topK int, categoryCounts map[string]int) error {
	if err := validateHeader(h); err != nil {
		return err
	}

	var report strings.Builder
	fmt.Fprintf(&report, "built_at: %s\n", h.BuiltAt)
	fmt.Fprintf(&report, "resources_sha256: %s\n", h.ResourcesSHA[:8])
	fmt.Fprintf(&report, "embed_model: %s\n", h.EmbedModel)
	fmt.Fprintf(&report, "pool: %s\n", h.Pool)
	fmt.Fprintf(&report, "generated_at: %s\n", h.GeneratedAt)
	fmt.Fprintf(&report, "retrieve_ms: %s\n\n", h.RetrieveMs)
	report.WriteString("| system | fixture | lane | set | k | orig_recall_off | orig_recall_shuf | orig_recall_on | orig_mrr_off | orig_mrr_shuf | orig_mrr_on | para_recall_off | para_recall_shuf | para_recall_on | para_mrr_off | para_mrr_shuf | para_mrr_on | distractors_off | distractors_shuf | distractors_on |\n")
	report.WriteString("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, row := range rows {
		fmt.Fprintf(
			&report,
			"| %s | %s | %s | %s | %d",
			escapeCell(row.System),
			escapeCell(row.Fixture),
			escapeCell(row.Lane),
			escapeCell(row.Set),
			row.K,
		)
		for _, metric := range row.Metrics {
			for _, cell := range metric.cells() {
				fmt.Fprintf(&report, " | %s", renderCell(cell))
			}
		}
		report.WriteString(" |\n")
	}
	report.WriteString("|  | mean |  |  | ")
	for metricIndex := range metricCount {
		for armIndex := range 3 {
			fmt.Fprintf(&report, " | %s", meanCell(rows, metricIndex, armIndex))
		}
	}
	report.WriteString(" |\n")

	report.WriteString("\n## Mean by k\n\n")
	report.WriteString("| k | orig_recall_off | orig_recall_shuf | orig_recall_on | para_recall_off | para_recall_shuf | para_recall_on |\n")
	report.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, k := range []int{1, 3, topK} {
		fmt.Fprintf(&report, "| k=%d", k)
		for _, original := range []bool{true, false} {
			for armIndex := range 3 {
				fmt.Fprintf(&report, " | %s", meanRecallAtK(rows, k, original, armIndex))
			}
		}
		report.WriteString(" |\n")
	}

	report.WriteString("\n## Distractors by category\n\n")
	report.WriteString("| category | n | distractors_off | distractors_shuf | distractors_on |\n")
	report.WriteString("| --- | ---: | ---: | ---: | ---: |\n")
	for _, category := range []string{"scope", "version", "responsibility", "lexical", "neighbor"} {
		fmt.Fprintf(&report, "| %s | %d", escapeCell(category), categoryCounts[category])
		for armIndex := range 3 {
			fmt.Fprintf(&report, " | %s", meanCategory(rows, category, armIndex))
		}
		report.WriteString(" |\n")
	}

	if _, err := io.WriteString(w, report.String()); err != nil {
		return fmt.Errorf("render report: write: %w", err)
	}
	return nil
}

func validateHeader(h Header) error {
	if _, err := time.Parse(time.RFC3339, h.BuiltAt); err != nil {
		return fmt.Errorf("invalid built_at metadata")
	}
	if !resourcesSHA256Pattern.MatchString(h.ResourcesSHA) {
		return fmt.Errorf("invalid resources_sha256 metadata")
	}
	if !printableASCII(h.EmbedModel, 64) {
		return fmt.Errorf("invalid embed_model metadata")
	}
	if h.Pool != reportPool {
		return fmt.Errorf("invalid pool metadata")
	}
	if _, err := time.Parse(time.RFC3339, h.GeneratedAt); err != nil {
		return fmt.Errorf("invalid generated_at metadata")
	}
	if h.RetrieveMs != "" && !retrieveMsPattern.MatchString(h.RetrieveMs) {
		return fmt.Errorf("invalid retrieve_ms metadata")
	}
	return nil
}

func printableASCII(value string, maximum int) bool {
	if len(value) > maximum {
		return false
	}
	for i := range len(value) {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func escapeCell(value string) string {
	return strings.NewReplacer(
		"\r\n", `\n`,
		"\r", `\n`,
		"\n", `\n`,
		"|", `\|`,
	).Replace(value)
}

func renderCell(cell Cell) string {
	if cell.Degraded != "" {
		return "degraded: " + escapeCell(cell.Degraded)
	}
	if cell.Integer {
		return fmt.Sprintf("%d", int(math.Round(cell.Value)))
	}
	return fmt.Sprintf("%.2f", cell.Value)
}

// meanOfCells renders the mean of the non-degraded cells in the "%.2f (n=%d)"
// shape shared by the main table's mean row, "## Mean by k", and
// "## Distractors by category" — degraded cells (missing vectors, a
// rerank failure) are excluded from both the sum and n, and an all-degraded
// (or empty) input renders "- (n=0)".
func meanOfCells(cells []Cell) string {
	var sum float64
	count := 0
	for _, cell := range cells {
		if cell.Degraded != "" {
			continue
		}
		sum += cell.Value
		count++
	}
	if count == 0 {
		return "- (n=0)"
	}
	return fmt.Sprintf("%.2f (n=%d)", sum/float64(count), count)
}

func meanCell(rows []Row, metricIndex, armIndex int) string {
	cells := make([]Cell, len(rows))
	for i, row := range rows {
		cells[i] = row.Metrics[metricIndex].cells()[armIndex]
	}
	return meanOfCells(cells)
}

func meanRecallAtK(rows []Row, k int, original bool, armIndex int) string {
	var cells []Cell
	for _, row := range rows {
		recall, ok := row.RecallByK[k]
		if !ok {
			continue
		}
		triplet := recall.Para
		if original {
			triplet = recall.Orig
		}
		cells = append(cells, triplet.cells()[armIndex])
	}
	return meanOfCells(cells)
}

func meanCategory(rows []Row, category string, armIndex int) string {
	var cells []Cell
	for _, row := range rows {
		triplet, ok := row.Categories[category]
		if !ok {
			continue
		}
		cells = append(cells, triplet.cells()[armIndex])
	}
	return meanOfCells(cells)
}
