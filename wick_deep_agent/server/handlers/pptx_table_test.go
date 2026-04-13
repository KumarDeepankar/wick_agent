package handlers

import (
	"strings"
	"testing"
)

func TestMarkdownTableParsing(t *testing.T) {
	md := `# Metrics

Quarterly headline numbers.

| Metric  | Q1    | Q2    |
|---------|-------|-------|
| Users   | 1.2M  | 1.5M  |
| Revenue | $4.8M | $6.1M |
`
	deck := parseMarkdownSlides(md)
	if len(deck.Slides) != 1 {
		t.Fatalf("expected 1 slide, got %d", len(deck.Slides))
	}
	s := deck.Slides[0]
	if len(s.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(s.Tables))
	}
	tbl := s.Tables[0]
	if len(tbl.Header) != 3 || tbl.Header[0] != "Metric" || tbl.Header[2] != "Q2" {
		t.Errorf("header mis-parsed: %v", tbl.Header)
	}
	if len(tbl.Rows) != 2 {
		t.Fatalf("expected 2 body rows, got %d", len(tbl.Rows))
	}
	if tbl.Rows[0][0] != "Users" || tbl.Rows[1][2] != "$6.1M" {
		t.Errorf("row content mis-parsed: %v", tbl.Rows)
	}

	// Body should NOT contain the raw pipe lines — they were consumed as a table.
	for _, line := range s.Body {
		if strings.Contains(line, "|") {
			t.Errorf("table row leaked into body: %q", line)
		}
	}
}

func TestTableRenderedInExportedPPTX(t *testing.T) {
	md := `# Comparison

| Plan  | Price | Users |
|-------|-------|-------|
| Free  | $0    | 1     |
| Pro   | $19   | 10    |
| Team  | $49   | Unlim |
`
	deck := parseMarkdownSlides(md)
	data, err := generatePPTX(deck)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)
	slide := parts["ppt/slides/slide1.xml"]

	// Table should render as <a:tbl>, NOT as raw pipe text.
	if !strings.Contains(slide, "<a:tbl>") {
		t.Error("slide missing <a:tbl> element")
	}
	if !strings.Contains(slide, `name="Table 50"`) {
		t.Error("slide missing table graphic frame name")
	}
	// Header cell text should appear.
	if !strings.Contains(slide, ">Plan<") || !strings.Contains(slide, ">Price<") {
		t.Error("header cells missing from table xml")
	}
	// Body cell values should appear.
	if !strings.Contains(slide, ">Pro<") || !strings.Contains(slide, ">$49<") {
		t.Error("body cells missing from table xml")
	}
	// The raw pipe-wrapped header line should NOT appear as text anywhere.
	if strings.Contains(slide, "| Plan | Price") {
		t.Error("raw markdown pipe line leaked into slide xml")
	}
}

func TestTableWithBodyAndCharts(t *testing.T) {
	md := `# Kitchen Sink

Headline summary paragraph.

- Bullet one
- Bullet two

| Name | Value |
|------|-------|
| A    | 10    |
| B    | 20    |

` + "```chart" + `
type: bar
labels: [A, B]
data: [10, 20]
` + "```" + `
`
	deck := parseMarkdownSlides(md)
	s := deck.Slides[0]
	if len(s.Body) < 2 {
		t.Errorf("expected bullets to survive table detection, got body=%v", s.Body)
	}
	if len(s.Tables) != 1 {
		t.Errorf("expected 1 table, got %d", len(s.Tables))
	}
	if len(s.Charts) != 1 {
		t.Errorf("expected 1 chart, got %d", len(s.Charts))
	}

	data, err := generatePPTX(deck)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)
	slide := parts["ppt/slides/slide1.xml"]
	if !strings.Contains(slide, "<a:tbl>") {
		t.Error("kitchen-sink slide missing table element")
	}
	if !strings.Contains(slide, "graphicFrame") {
		t.Error("kitchen-sink slide missing chart graphic frame")
	}
}
