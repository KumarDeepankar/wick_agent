package handlers

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestGeneratePPTXWithBarChart(t *testing.T) {
	md := `# Revenue

Quarterly performance summary.

` + "```chart" + `
type: bar
title: Revenue by Quarter
labels: [Q1, Q2, Q3, Q4]
data: [100, 150, 200, 180]
` + "```" + `
`
	slides := parseMarkdownSlides(md)
	if len(slides) != 1 {
		t.Fatalf("expected 1 slide, got %d", len(slides))
	}
	if len(slides[0].Charts) != 1 {
		t.Fatalf("expected 1 chart on slide, got %d", len(slides[0].Charts))
	}
	chart := slides[0].Charts[0]
	if chart.Type != "bar" {
		t.Errorf("chart type: got %q want bar", chart.Type)
	}
	if len(chart.Series) != 1 || len(chart.Series[0].Data) != 4 {
		t.Errorf("series mis-parsed: %+v", chart.Series)
	}

	data, err := generatePPTX(slides)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)

	mustExist := []string{
		"[Content_Types].xml",
		"ppt/presentation.xml",
		"ppt/slides/slide1.xml",
		"ppt/slides/_rels/slide1.xml.rels",
		"ppt/charts/chart1.xml",
		"ppt/charts/_rels/chart1.xml.rels",
		"ppt/embeddings/Microsoft_Excel_Worksheet1.xlsx",
	}
	for _, p := range mustExist {
		if _, ok := parts[p]; !ok {
			t.Errorf("missing part: %s", p)
		}
	}

	if !strings.Contains(parts["[Content_Types].xml"], "/ppt/charts/chart1.xml") {
		t.Error("content types missing chart override")
	}
	if !strings.Contains(parts["[Content_Types].xml"], "Microsoft_Excel_Worksheet1.xlsx") {
		t.Error("content types missing embedded xlsx override")
	}
	if !strings.Contains(parts["ppt/slides/_rels/slide1.xml.rels"], "../charts/chart1.xml") {
		t.Error("slide1 rels missing chart relationship")
	}
	if !strings.Contains(parts["ppt/charts/_rels/chart1.xml.rels"], "Microsoft_Excel_Worksheet1.xlsx") {
		t.Error("chart1 rels missing embedded xlsx relationship")
	}
	if !strings.Contains(parts["ppt/slides/slide1.xml"], "graphicFrame") {
		t.Error("slide1 missing graphicFrame")
	}
	chartXML := parts["ppt/charts/chart1.xml"]
	if !strings.Contains(chartXML, "<c:barChart>") {
		t.Error("chart1.xml missing barChart element")
	}
	if !strings.Contains(chartXML, `<c:externalData r:id="rId1">`) {
		t.Error("chart1.xml missing externalData reference to workbook")
	}
	if !strings.Contains(chartXML, "<c:v>Q1</c:v>") || !strings.Contains(chartXML, "<c:v>200</c:v>") {
		t.Error("chart1.xml missing cached labels/data")
	}

	// The embedded xlsx must itself be a valid zip with workbook + sheet1.
	xlsxBytes := []byte(parts["ppt/embeddings/Microsoft_Excel_Worksheet1.xlsx"])
	xlsxParts := unzipParts(t, xlsxBytes)
	for _, p := range []string{"[Content_Types].xml", "xl/workbook.xml", "xl/worksheets/sheet1.xml", "xl/styles.xml"} {
		if _, ok := xlsxParts[p]; !ok {
			t.Errorf("embedded xlsx missing part: %s", p)
		}
	}
	sheet := xlsxParts["xl/worksheets/sheet1.xml"]
	if !strings.Contains(sheet, ">Q1<") || !strings.Contains(sheet, ">200<") {
		t.Errorf("sheet1 missing label/value cells: %s", sheet)
	}
}

func TestGeneratePPTXAdditionalChartTypes(t *testing.T) {
	cases := []struct {
		name      string
		dslType   string
		wantElem  string
		extraData string // for stacked_bar series block
	}{
		{"area", "area", "<c:areaChart>", ""},
		{"donut", "donut", "<c:doughnutChart>", ""},
		{"stacked_bar", "stacked_bar", `<c:grouping val="stacked"/>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := "# Slide\n\n```chart\ntype: " + tc.dslType + "\nlabels: [A, B, C]\nseries:\n  - name: S1\n    data: [10, 20, 30]\n  - name: S2\n    data: [5, 15, 25]\n```\n"
			slides := parseMarkdownSlides(md)
			if len(slides) != 1 || len(slides[0].Charts) != 1 {
				t.Fatalf("expected 1 slide with 1 chart, got %d slides", len(slides))
			}
			data, err := generatePPTX(slides)
			if err != nil {
				t.Fatalf("generatePPTX: %v", err)
			}
			parts := unzipParts(t, data)
			chartXML := parts["ppt/charts/chart1.xml"]
			if !strings.Contains(chartXML, tc.wantElem) {
				t.Errorf("chart1.xml missing %s; got: %s", tc.wantElem, chartXML)
			}
			if _, ok := parts["ppt/embeddings/Microsoft_Excel_Worksheet1.xlsx"]; !ok {
				t.Error("embedded workbook missing")
			}
		})
	}
}

func TestGeneratePPTXWithLineAndPie(t *testing.T) {
	md := `# Trends

` + "```chart" + `
type: line
labels: [Jan, Feb, Mar]
series:
  - name: 2024
    data: [10, 20, 15]
  - name: 2025
    data: [12, 22, 18]
` + "```" + `

---

# Mix

` + "```chart" + `
type: pie
labels: [Apples, Pears, Plums]
data: [30, 50, 20]
` + "```" + `
`
	slides := parseMarkdownSlides(md)
	if len(slides) != 2 {
		t.Fatalf("expected 2 slides, got %d", len(slides))
	}

	data, err := generatePPTX(slides)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)

	if !strings.Contains(parts["ppt/charts/chart1.xml"], "<c:lineChart>") {
		t.Error("chart1 should be lineChart")
	}
	if !strings.Contains(parts["ppt/charts/chart2.xml"], "<c:pieChart>") {
		t.Error("chart2 should be pieChart")
	}
	if !strings.Contains(parts["ppt/slides/_rels/slide2.xml.rels"], "../charts/chart2.xml") {
		t.Error("slide2 rels missing chart2 relationship")
	}
}

func TestUnsupportedChartFallsBackToText(t *testing.T) {
	md := `# Bubble

` + "```chart" + `
type: bubble
labels: [A, B]
data: [1, 2]
` + "```" + `
`
	slides := parseMarkdownSlides(md)
	if len(slides) != 1 {
		t.Fatalf("expected 1 slide")
	}
	if len(slides[0].Charts) != 0 {
		t.Errorf("expected 0 native charts for bubble, got %d", len(slides[0].Charts))
	}
	found := false
	for _, b := range slides[0].Body {
		if strings.Contains(b, "bubble") && strings.Contains(b, "unsupported") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fallback text in body, got %v", slides[0].Body)
	}
}

func unzipParts(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(b)
	}
	return out
}
