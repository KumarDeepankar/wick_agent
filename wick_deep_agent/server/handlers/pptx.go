package handlers

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// slideContent holds parsed slide data for PPTX generation.
type slideContent struct {
	Title  string
	Body   []string       // paragraphs / bullet points
	Charts []*ChartConfig // native charts parsed from ```chart fences
	Tables []*TableData   // pipe tables parsed from the slide body
	Layout string         // title|section|content|content_chart|two_column
	Col1   []string       // two-column left, populated only when Layout=two_column
	Col2   []string       // two-column right
}

// parsedDeck is the result of parsing a slides markdown file: a deck-wide
// theme name plus the per-slide content.
type parsedDeck struct {
	Theme  string
	Slides []slideContent
}

var themeDirective = regexp.MustCompile(`(?m)^\s*<!--\s*theme:\s*([a-zA-Z0-9_-]+)\s*-->\s*\n?`)
var layoutDirective = regexp.MustCompile(`(?m)^\s*<!--\s*layout:\s*([a-zA-Z0-9_-]+)\s*-->\s*\n?`)

// parseMarkdownSlides splits markdown slide content into structured slides
// and extracts the optional <!-- theme: name --> directive.
func parseMarkdownSlides(content string) parsedDeck {
	// Extract <!-- theme: X --> directive (first match wins) and strip it
	// from the content so it doesn't end up as slide text.
	deck := parsedDeck{}
	if m := themeDirective.FindStringSubmatch(content); len(m) == 2 {
		deck.Theme = m[1]
		content = themeDirective.ReplaceAllString(content, "")
	}

	// Strip <!-- slides --> marker
	content = regexp.MustCompile(`(?m)^\s*<!--\s*slides\s*-->\s*\n?`).ReplaceAllString(content, "")

	raw := strings.Split(content, "\n---\n")
	for _, block := range raw {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		s := slideContent{}

		// Per-slide layout directive: <!-- layout: section -->
		if m := layoutDirective.FindStringSubmatch(block); len(m) == 2 {
			s.Layout = m[1]
			block = layoutDirective.ReplaceAllString(block, "")
		}

		lines := strings.Split(block, "\n")
		bodyStart := 0

		// Extract title from first heading
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "# ") {
				s.Title = stripMarkdown(strings.TrimPrefix(trimmed, "# "))
				bodyStart = i + 1
				break
			}
			if strings.HasPrefix(trimmed, "## ") {
				s.Title = stripMarkdown(strings.TrimPrefix(trimmed, "## "))
				bodyStart = i + 1
				break
			}
		}

		// `target` is the slice that paragraph/bullet/heading lines append to.
		// It points at s.Body by default and at s.Col1/s.Col2 inside :::col1
		// / :::col2 fenced divs, so the same accumulation logic serves both.
		target := &s.Body
		flushPara := func(para *strings.Builder) {
			if para.Len() > 0 {
				*target = append(*target, stripMarkdown(para.String()))
				para.Reset()
			}
		}

		var para strings.Builder
		inCode := false
		codeFenceLang := ""
		var codeBuf strings.Builder

		// Indexed loop so we can peek ahead at lines[i+1] when detecting
		// markdown pipe-table header/separator pairs.
		for i := bodyStart; i < len(lines); i++ {
			line := lines[i]
			trimmed := strings.TrimSpace(line)

			// :::col1 / :::col2 / ::: fenced divs (pandoc-style).
			if trimmed == ":::col1" || trimmed == ":::col2" {
				flushPara(&para)
				if trimmed == ":::col1" {
					target = &s.Col1
				} else {
					target = &s.Col2
				}
				if s.Layout == "" {
					s.Layout = "two_column"
				}
				continue
			}
			if trimmed == ":::" {
				flushPara(&para)
				target = &s.Body
				continue
			}

			// Fenced blocks: capture ```chart bodies as native charts; drop other code.
			if strings.HasPrefix(trimmed, "```") {
				if inCode {
					if codeFenceLang == "chart" {
						chart := parseChartDSL(codeBuf.String())
						if chartTypeIsNative(chart.Type) && (len(chart.Series) > 0 || len(chart.Data) > 0) {
							s.Charts = append(s.Charts, chart)
						} else {
							*target = append(*target, fmt.Sprintf("[Chart: %s — unsupported in export]", chart.Type))
						}
					}
					inCode = false
					codeFenceLang = ""
					codeBuf.Reset()
				} else {
					inCode = true
					codeFenceLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				}
				continue
			}
			if inCode {
				if codeFenceLang == "chart" {
					codeBuf.WriteString(line)
					codeBuf.WriteString("\n")
				}
				continue
			}

			// Pipe-table detection: a line starting with `|` followed by a
			// `|---|---|` separator line. Tables parse into structured
			// TableData and get rendered as a native OOXML <a:tbl> in the
			// slide XML — NOT as raw pipe text.
			if isTableHeader(lines, i) {
				flushPara(&para)
				tbl, next := parseTableBlock(lines, i)
				s.Tables = append(s.Tables, tbl)
				i = next - 1 // for-loop will increment
				continue
			}

			// Skip sub-headings (include as body text)
			if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "## ") {
				flushPara(&para)
				*target = append(*target, stripMarkdown(strings.TrimLeft(trimmed, "# ")))
				continue
			}

			// Bullet points
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
				flushPara(&para)
				text := strings.TrimPrefix(trimmed, "- ")
				text = strings.TrimPrefix(text, "* ")
				*target = append(*target, "• "+stripMarkdown(text))
				continue
			}

			// Numbered list
			if matched, _ := regexp.MatchString(`^\d+\.\s`, trimmed); matched {
				flushPara(&para)
				*target = append(*target, stripMarkdown(trimmed))
				continue
			}

			// Empty line = paragraph break
			if trimmed == "" {
				flushPara(&para)
				continue
			}

			// Accumulate paragraph text
			if para.Len() > 0 {
				para.WriteString(" ")
			}
			para.WriteString(trimmed)
		}
		flushPara(&para)

		if s.Title == "" && len(s.Body) > 0 {
			s.Title = s.Body[0]
			s.Body = s.Body[1:]
		}

		deck.Slides = append(deck.Slides, s)
	}
	return deck
}

var mdStripPatterns = regexp.MustCompile(`\*\*([^*]+)\*\*|\*([^*]+)\*|_([^_]+)_|__([^_]+)__|` + "`" + `([^` + "`" + `]+)` + "`" + `|\[([^\]]+)\]\([^)]+\)`)

// stripMarkdown removes common markdown formatting.
func stripMarkdown(s string) string {
	s = mdStripPatterns.ReplaceAllStringFunc(s, func(match string) string {
		// Return the inner content without formatting markers
		sub := mdStripPatterns.FindStringSubmatch(match)
		for i := 1; i < len(sub); i++ {
			if sub[i] != "" {
				return sub[i]
			}
		}
		return match
	})
	return strings.TrimSpace(s)
}

// generatePPTX creates a designed PPTX file from a parsed deck. The deck's
// theme drives slide background, title color, accent stripe, footer style,
// and chart palette.
func generatePPTX(deck parsedDeck) ([]byte, error) {
	theme := ResolveTheme(deck.Theme)
	slides := deck.Slides

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// EMU constants (English Metric Units: 1 inch = 914400 EMU)
	// Slide: 10" x 7.5"

	// [Content_Types].xml
	// Assign global chart IDs (1-based) to each chart so we can reference them
	// from content types, slide rels, and the chart part files.
	totalCharts := 0
	chartStart := make([]int, len(slides)) // first global chart id for slide i
	for i, s := range slides {
		chartStart[i] = totalCharts + 1
		totalCharts += len(s.Charts)
	}

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>
  <Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>
  <Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>
  <Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>`
	for i := range slides {
		contentTypes += fmt.Sprintf(`
  <Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i+1)
	}
	for i := 1; i <= totalCharts; i++ {
		contentTypes += fmt.Sprintf(`
  <Override PartName="/ppt/charts/chart%d.xml" ContentType="application/vnd.openxmlformats-officedocument.drawingml.chart+xml"/>
  <Override PartName="/ppt/embeddings/Microsoft_Excel_Worksheet%d.xlsx" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"/>`, i, i)
	}
	contentTypes += `
</Types>`
	writeZipFile(zw, "[Content_Types].xml", contentTypes)

	// _rels/.rels
	writeZipFile(zw, "_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>
</Relationships>`)

	// ppt/presentation.xml
	sldIdList := "<p:sldIdLst>"
	for i := range slides {
		sldIdList += fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 256+i, 10+i)
	}
	sldIdList += "</p:sldIdLst>"

	writeZipFile(zw, "ppt/presentation.xml", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
  xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
  xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>
  %s
  <p:sldSz cx="9144000" cy="6858000" type="screen4x3"/>
  <p:notesSz cx="6858000" cy="9144000"/>
</p:presentation>`, sldIdList))

	// ppt/_rels/presentation.xml.rels
	presRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="theme/theme1.xml"/>`
	for i := range slides {
		presRels += fmt.Sprintf(`
  <Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, 10+i, i+1)
	}
	presRels += `
</Relationships>`
	writeZipFile(zw, "ppt/_rels/presentation.xml.rels", presRels)

	// Theme + master + layout — all driven by the resolved theme.
	writeZipFile(zw, "ppt/theme/theme1.xml", buildThemeXML(theme))

	writeZipFile(zw, "ppt/slideMasters/slideMaster1.xml", buildSlideMasterXML(theme))
	writeZipFile(zw, "ppt/slideMasters/_rels/slideMaster1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/>
</Relationships>`)

	writeZipFile(zw, "ppt/slideLayouts/slideLayout1.xml", buildSlideLayoutXML(theme))
	writeZipFile(zw, "ppt/slideLayouts/_rels/slideLayout1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/>
</Relationships>`)

	// Slides
	for i, slide := range slides {
		writeZipFile(zw, fmt.Sprintf("ppt/slides/slide%d.xml", i+1), buildSlideXML(slide, chartStart[i], theme))

		// Per-slide rels: layout + one entry per chart on this slide.
		rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>`
		for j := range slide.Charts {
			globalID := chartStart[i] + j
			rels += fmt.Sprintf(`
  <Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chart" Target="../charts/chart%d.xml"/>`, j+2, globalID)
		}
		rels += `
</Relationships>`
		writeZipFile(zw, fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), rels)
	}

	// Chart parts: chart XML + per-chart rels pointing at the embedded
	// workbook + the workbook itself. The chart's <c:externalData r:id="rId1"/>
	// is the hook PowerPoint follows when the user clicks "Edit Data".
	for i, slide := range slides {
		for j, chart := range slide.Charts {
			globalID := chartStart[i] + j
			writeZipFile(zw, fmt.Sprintf("ppt/charts/chart%d.xml", globalID), buildChartXML(chart, theme))

			rels := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/package" Target="../embeddings/Microsoft_Excel_Worksheet%d.xlsx"/>
</Relationships>`, globalID)
			writeZipFile(zw, fmt.Sprintf("ppt/charts/_rels/chart%d.xml.rels", globalID), rels)

			xlsxBytes, err := buildEmbeddedXLSX(chart)
			if err != nil {
				return nil, fmt.Errorf("build embedded xlsx for chart %d: %w", globalID, err)
			}
			writeZipBytes(zw, fmt.Sprintf("ppt/embeddings/Microsoft_Excel_Worksheet%d.xlsx", globalID), xlsxBytes)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeZipFile(zw *zip.Writer, name, content string) {
	w, _ := zw.Create(name)
	w.Write([]byte(content))
}

func writeZipBytes(zw *zip.Writer, name string, content []byte) {
	w, _ := zw.Create(name)
	w.Write(content)
}

// buildSlideXML dispatches to a layout-specific builder based on s.Layout.
// Per-slide chart relationships start at rId2 (rId1 is the slideLayout).
// chartIDBase is the global chart ID assigned to this slide's first chart.
func buildSlideXML(s slideContent, chartIDBase int, theme *Theme) string {
	switch s.Layout {
	case "title":
		return buildTitleSlide(s, theme)
	case "section":
		return buildSectionSlide(s, theme)
	case "two_column":
		return buildTwoColumnSlide(s, theme)
	case "content_chart":
		return buildContentChartSlide(s, chartIDBase, theme)
	default:
		return buildContentSlide(s, chartIDBase, theme)
	}
}

// buildContentSlide is the default Title + body + optional charts/tables layout.
func buildContentSlide(s slideContent, chartIDBase int, theme *Theme) string {
	titleEsc := html.EscapeString(s.Title)
	hasCharts := len(s.Charts) > 0
	hasTables := len(s.Tables) > 0

	// Body box height: shrinks to make room for charts and/or tables below.
	// Layout zones from top (EMU):
	//   title:   274638 .. 1417638
	//   body:    1600200 .. bodyEnd
	//   tables:  just below body (if any)
	//   charts:  bottom region (if any)
	bodyY, bodyCY := 1600200, 4525963
	if hasCharts || hasTables {
		bodyCY = 1400000
	}

	var bodyParas string
	for _, para := range s.Body {
		bodyParas += fmt.Sprintf(`
            <a:p>
              <a:r><a:rPr lang="en-US" sz="1800" dirty="0"><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:latin typeface="%s"/></a:rPr><a:t>%s</a:t></a:r>
            </a:p>`, theme.BodyColor, theme.BodyFont, html.EscapeString(para))
	}
	if bodyParas == "" {
		bodyParas = `<a:p><a:endParaRPr lang="en-US"/></a:p>`
	}

	// Tables go directly below body text, charts (if any) below tables.
	tablesXML := ""
	tablesEndY := bodyY + bodyCY
	if hasTables {
		const (
			tblMarginX  = 457200
			tblRowWidth = 9144000 - 2*457200
			tblGap      = 150000
		)
		// Split remaining vertical space between tables evenly. If charts
		// are also present, cap the table region so charts get at least
		// 2.8M EMU (~3") at the bottom.
		tblYStart := bodyY + bodyCY + 150000
		tblRegionMax := 6858000 - tblYStart - 250000
		if hasCharts {
			tblRegionMax -= 2800000
		}
		if tblRegionMax < 800000 {
			tblRegionMax = 800000
		}
		nT := len(s.Tables)
		tblCY := (tblRegionMax - tblGap*(nT-1)) / nT
		if tblCY < 700000 {
			tblCY = 700000
		}
		for j, t := range s.Tables {
			y := tblYStart + j*(tblCY+tblGap)
			tablesXML += buildTableFrame(t, theme, 50+j, tblMarginX, y, tblRowWidth, tblCY)
		}
		tablesEndY = tblYStart + nT*tblCY + (nT-1)*tblGap
	}

	// Lay charts out in a single horizontal row beneath the body/tables.
	chartsXML := ""
	if hasCharts {
		const (
			rowCY    = 3000000
			marginX  = 457200
			gutter   = 200000
			rowWidth = 9144000 - 2*457200
		)
		rowY := tablesEndY + 200000
		if rowY+rowCY > 6550000 {
			rowY = 6550000 - rowCY
		}
		n := len(s.Charts)
		chartCX := (rowWidth - gutter*(n-1)) / n
		nextID := 100 // shape ids unique within the slide
		for j := range s.Charts {
			x := marginX + j*(chartCX+gutter)
			rID := j + 2 // rId1 = layout, charts start at rId2
			chartsXML += fmt.Sprintf(`
      <p:graphicFrame>
        <p:nvGraphicFramePr>
          <p:cNvPr id="%d" name="Chart %d"/>
          <p:cNvGraphicFramePr/>
          <p:nvPr/>
        </p:nvGraphicFramePr>
        <p:xfrm>
          <a:off x="%d" y="%d"/>
          <a:ext cx="%d" cy="%d"/>
        </p:xfrm>
        <a:graphic>
          <a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart">
            <c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" r:id="rId%d"/>
          </a:graphicData>
        </a:graphic>
      </p:graphicFrame>`, nextID+j, chartIDBase+j, x, rowY, chartCX, rowCY, rID)
		}
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
  xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
  xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:cSld>
    <p:spTree>
      <p:nvGrpSpPr>
        <p:cNvPr id="1" name=""/>
        <p:cNvGrpSpPr/>
        <p:nvPr/>
      </p:nvGrpSpPr>
      <p:grpSpPr>
        <a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm>
      </p:grpSpPr>
      <p:sp>
        <p:nvSpPr>
          <p:cNvPr id="2" name="Title"/>
          <p:cNvSpPr txBox="1"/>
          <p:nvPr/>
        </p:nvSpPr>
        <p:spPr>
          <a:xfrm><a:off x="457200" y="274638"/><a:ext cx="8229600" cy="1143000"/></a:xfrm>
          <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
          <a:noFill/>
        </p:spPr>
        <p:txBody>
          <a:bodyPr wrap="square" anchor="b"/>
          <a:lstStyle/>
          <a:p>
            <a:r><a:rPr lang="en-US" sz="3200" b="1" dirty="0"><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:latin typeface="%s"/></a:rPr><a:t>%s</a:t></a:r>
          </a:p>
        </p:txBody>
      </p:sp>
      <p:sp>
        <p:nvSpPr>
          <p:cNvPr id="3" name="Content"/>
          <p:cNvSpPr txBox="1"/>
          <p:nvPr/>
        </p:nvSpPr>
        <p:spPr>
          <a:xfrm><a:off x="457200" y="%d"/><a:ext cx="8229600" cy="%d"/></a:xfrm>
          <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
          <a:noFill/>
        </p:spPr>
        <p:txBody>
          <a:bodyPr wrap="square" anchor="t"/>
          <a:lstStyle/>%s
        </p:txBody>
      </p:sp>%s%s
    </p:spTree>
  </p:cSld>
</p:sld>`, theme.TitleColor, theme.TitleFont, titleEsc, bodyY, bodyCY, bodyParas, tablesXML, chartsXML)
}

