package retrievaleval

import (
	"fmt"
	"io"
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

var resourcesSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

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

func (t Triplet) cells() [3]Cell {
	return [3]Cell{t.Off, t.Shuf, t.On}
}

type Row struct {
	Fixture string
	Lane    string
	Set     string
	K       int
	Metrics [metricCount]Triplet
}

type Header struct {
	BuiltAt      string
	ResourcesSHA string
	EmbedModel   string
	Pool         string
	GeneratedAt  string
}

// Render writes the retrieval-quality report.
func Render(w io.Writer, h Header, rows []Row) error {
	if err := validateHeader(h); err != nil {
		return err
	}

	var report strings.Builder
	fmt.Fprintf(&report, "built_at: %s\n", h.BuiltAt)
	fmt.Fprintf(&report, "resources_sha256: %s\n", h.ResourcesSHA[:8])
	fmt.Fprintf(&report, "embed_model: %s\n", h.EmbedModel)
	fmt.Fprintf(&report, "pool: %s\n", h.Pool)
	fmt.Fprintf(&report, "generated_at: %s\n\n", h.GeneratedAt)
	report.WriteString("| fixture | lane | set | k | orig_recall_off | orig_recall_shuf | orig_recall_on | orig_mrr_off | orig_mrr_shuf | orig_mrr_on | para_recall_off | para_recall_shuf | para_recall_on | para_mrr_off | para_mrr_shuf | para_mrr_on | distractors_off | distractors_shuf | distractors_on |\n")
	report.WriteString("| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, row := range rows {
		fmt.Fprintf(
			&report,
			"| %s | %s | %s | %d",
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
	report.WriteString("| mean |  |  | ")
	for metricIndex := range metricCount {
		for armIndex := range 3 {
			fmt.Fprintf(&report, " | %s", meanCell(rows, metricIndex, armIndex))
		}
	}
	report.WriteString(" |\n")

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
		return fmt.Sprintf("%d", int(cell.Value))
	}
	return fmt.Sprintf("%.2f", cell.Value)
}

func meanCell(rows []Row, metricIndex, armIndex int) string {
	var sum float64
	count := 0
	for _, row := range rows {
		cell := row.Metrics[metricIndex].cells()[armIndex]
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
