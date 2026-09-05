package retrievaleval

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const reportPool = "off=TopK+1 on=4xTopK"

var resourcesSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Cell struct {
	Value    float64
	Degraded string
}

type Row struct {
	Fixture   string
	Lane      string
	Set       string
	K         int
	RecallOff Cell
	RecallOn  Cell
	MRROff    Cell
	MRROn     Cell
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
	report.WriteString("| fixture | lane | set | k | recall_off | recall_on | mrr_off | mrr_on |\n")
	report.WriteString("| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, row := range rows {
		fmt.Fprintf(
			&report,
			"| %s | %s | %s | %d | %s | %s | %s | %s |\n",
			escapeCell(row.Fixture),
			escapeCell(row.Lane),
			escapeCell(row.Set),
			row.K,
			renderCell(row.RecallOff),
			renderCell(row.RecallOn),
			renderCell(row.MRROff),
			renderCell(row.MRROn),
		)
	}
	fmt.Fprintf(
		&report,
		"| mean |  |  |  | %s | %s | %s | %s |\n",
		meanCell(rows, func(row Row) Cell { return row.RecallOff }, false),
		meanCell(rows, func(row Row) Cell { return row.RecallOn }, true),
		meanCell(rows, func(row Row) Cell { return row.MRROff }, false),
		meanCell(rows, func(row Row) Cell { return row.MRROn }, true),
	)

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
	return fmt.Sprintf("%.2f", cell.Value)
}

func meanCell(rows []Row, selectCell func(Row) Cell, withCount bool) string {
	var sum float64
	count := 0
	for _, row := range rows {
		cell := selectCell(row)
		if cell.Degraded != "" {
			continue
		}
		sum += cell.Value
		count++
	}
	if withCount {
		if count == 0 {
			return "- (n=0)"
		}
		return fmt.Sprintf("%.2f (n=%d)", sum/float64(count), count)
	}
	if count == 0 {
		return "0.00"
	}
	return fmt.Sprintf("%.2f", sum/float64(count))
}
