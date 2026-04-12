package handlers

import (
	"strings"
	"testing"
)

func TestTitleSlideLayout(t *testing.T) {
	md := `<!-- layout: title -->
# Annual Report 2026

A look at the year ahead
`
	deck := parseMarkdownSlides(md)
	if len(deck.Slides) != 1 {
		t.Fatalf("expected 1 slide")
	}
	if deck.Slides[0].Layout != "title" {
		t.Errorf("layout: got %q want title", deck.Slides[0].Layout)
	}
	data, err := generatePPTX(deck)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)
	slide := parts["ppt/slides/slide1.xml"]

	// Title slide should center its title and use the large 5400 size.
	if !strings.Contains(slide, `algn="ctr"`) {
		t.Error("title slide should be centered")
	}
	if !strings.Contains(slide, `sz="5400"`) {
		t.Error("title slide should use 54pt title")
	}
	if !strings.Contains(slide, "Annual Report 2026") {
		t.Error("title text missing")
	}
	if !strings.Contains(slide, "A look at the year ahead") {
		t.Error("subtitle missing")
	}
	// Should NOT contain a graphicFrame (no charts on title slide)
	if strings.Contains(slide, "graphicFrame") {
		t.Error("title slide unexpectedly contains a chart frame")
	}
}

func TestSectionSlideLayout(t *testing.T) {
	md := `<!-- layout: section -->
# Findings

Part Two
`
	deck := parseMarkdownSlides(md)
	if deck.Slides[0].Layout != "section" {
		t.Fatalf("layout: got %q want section", deck.Slides[0].Layout)
	}
	data, err := generatePPTX(deck)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)
	slide := parts["ppt/slides/slide1.xml"]

	if !strings.Contains(slide, "Findings") {
		t.Error("section title missing")
	}
	// Section kicker is upper-cased from the first body line.
	if !strings.Contains(slide, "PART TWO") {
		t.Error("section kicker should be upper-cased PART TWO")
	}
	// Section bar shape should exist (a rectangle on the left margin).
	if !strings.Contains(slide, `name="Section Bar"`) {
		t.Error("section bar shape missing")
	}
}

func TestTwoColumnSlideLayoutFromDivs(t *testing.T) {
	md := `<!-- layout: two_column -->
# Comparison

:::col1
- Pros A
- Pros B
:::

:::col2
- Cons A
- Cons B
:::
`
	deck := parseMarkdownSlides(md)
	s := deck.Slides[0]
	if s.Layout != "two_column" {
		t.Fatalf("layout: got %q want two_column", s.Layout)
	}
	if len(s.Col1) != 2 || len(s.Col2) != 2 {
		t.Fatalf("expected 2 entries in each column, got col1=%v col2=%v", s.Col1, s.Col2)
	}
	if !strings.Contains(s.Col1[0], "Pros A") || !strings.Contains(s.Col2[1], "Cons B") {
		t.Errorf("column content mis-routed: col1=%v col2=%v", s.Col1, s.Col2)
	}

	data, err := generatePPTX(deck)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)
	slide := parts["ppt/slides/slide1.xml"]
	if !strings.Contains(slide, `name="Column 1"`) || !strings.Contains(slide, `name="Column 2"`) {
		t.Error("expected two named column text boxes in slide xml")
	}
	if !strings.Contains(slide, "Pros A") || !strings.Contains(slide, "Cons B") {
		t.Error("column text missing from slide xml")
	}
}

func TestTwoColumnAutoSplitWhenNoDivs(t *testing.T) {
	// Layout directive but no :::col fences — body should be split in half.
	md := `<!-- layout: two_column -->
# Auto

- one
- two
- three
- four
`
	deck := parseMarkdownSlides(md)
	s := deck.Slides[0]
	if len(s.Col1) != 0 || len(s.Col2) != 0 {
		t.Errorf("expected col1/col2 empty before fallback, got %v %v", s.Col1, s.Col2)
	}
	if len(s.Body) != 4 {
		t.Errorf("expected 4 body entries, got %v", s.Body)
	}
	// Smoke-test that the layout still renders without panicking and includes
	// content from both halves.
	data, err := generatePPTX(deck)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	slide := unzipParts(t, data)["ppt/slides/slide1.xml"]
	if !strings.Contains(slide, "one") || !strings.Contains(slide, "four") {
		t.Error("auto-split column content missing")
	}
}

func TestContentChartLayout(t *testing.T) {
	md := `<!-- layout: content_chart -->
# Revenue

Quarterly performance overview.

` + "```chart" + `
type: bar
labels: [Q1, Q2, Q3, Q4]
data: [100, 150, 200, 180]
` + "```" + `
`
	deck := parseMarkdownSlides(md)
	if deck.Slides[0].Layout != "content_chart" {
		t.Fatalf("layout: got %q want content_chart", deck.Slides[0].Layout)
	}
	data, err := generatePPTX(deck)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)
	slide := parts["ppt/slides/slide1.xml"]
	if !strings.Contains(slide, "graphicFrame") {
		t.Error("content_chart slide should contain chart graphicFrame")
	}
	if !strings.Contains(slide, "Quarterly performance overview.") {
		t.Error("caption text missing")
	}
}

func TestColumnFenceImpliesTwoColumnLayout(t *testing.T) {
	// No explicit layout directive, but :::col fences are used — parser
	// should set Layout=two_column automatically.
	md := `# Implicit

:::col1
left
:::
:::col2
right
:::
`
	deck := parseMarkdownSlides(md)
	if deck.Slides[0].Layout != "two_column" {
		t.Errorf("expected layout to auto-promote to two_column, got %q", deck.Slides[0].Layout)
	}
}

func TestUnknownLayoutFallsBackToContent(t *testing.T) {
	md := `<!-- layout: nonexistent -->
# Plain

Body line.
`
	deck := parseMarkdownSlides(md)
	if deck.Slides[0].Layout != "nonexistent" {
		t.Errorf("layout directive should be preserved as-is, got %q", deck.Slides[0].Layout)
	}
	// Should still render successfully via the default content builder.
	if _, err := generatePPTX(deck); err != nil {
		t.Errorf("generatePPTX failed for unknown layout: %v", err)
	}
}
