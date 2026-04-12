package handlers

import (
	"fmt"
	"html"
	"strings"
)

// Layout EMU constants. Slide is 9144000 × 6858000 (10" × 7.5" at 914400 EMU/in).
const (
	slideW = 9144000
	slideH = 6858000
)

// runText is the standard <a:r> wrapper for a single styled text run. Pulled
// out so each layout builder doesn't repeat the rPr formatting.
func runText(text string, sizeHundredths int, bold bool, hexColor, font string) string {
	b := ""
	if bold {
		b = ` b="1"`
	}
	return fmt.Sprintf(
		`<a:r><a:rPr lang="en-US" sz="%d"%s dirty="0"><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:latin typeface="%s"/></a:rPr><a:t>%s</a:t></a:r>`,
		sizeHundredths, b, hexColor, font, html.EscapeString(text))
}

// textBox emits a standalone <p:sp> text box at the given EMU position with
// pre-built paragraph XML inside.
func textBox(id int, name string, x, y, cx, cy int, anchor string, paragraphs string) string {
	return fmt.Sprintf(`<p:sp>
        <p:nvSpPr>
          <p:cNvPr id="%d" name="%s"/>
          <p:cNvSpPr txBox="1"/>
          <p:nvPr/>
        </p:nvSpPr>
        <p:spPr>
          <a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>
          <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
          <a:noFill/>
        </p:spPr>
        <p:txBody>
          <a:bodyPr wrap="square" anchor="%s"/>
          <a:lstStyle/>
          %s
        </p:txBody>
      </p:sp>`, id, name, x, y, cx, cy, anchor, paragraphs)
}

// rectShape emits a solid-color rectangle (used for accent stripes / hero
// blocks). algn is unused for shapes; this is just <p:sp> with a fill.
func rectShape(id int, name string, x, y, cx, cy int, hexFill string) string {
	return fmt.Sprintf(`<p:sp>
        <p:nvSpPr>
          <p:cNvPr id="%d" name="%s"/>
          <p:cNvSpPr/>
          <p:nvPr/>
        </p:nvSpPr>
        <p:spPr>
          <a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>
          <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
          <a:solidFill><a:srgbClr val="%s"/></a:solidFill>
          <a:ln><a:noFill/></a:ln>
        </p:spPr>
        <p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p></p:txBody>
      </p:sp>`, id, name, x, y, cx, cy, hexFill)
}

// wrapSlide returns the full slide XML envelope around a given inner spTree
// children string. All layouts share this scaffold so we don't keep
// re-typing the namespaces and group shape boilerplate.
func wrapSlide(spChildren string) string {
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
      %s
    </p:spTree>
  </p:cSld>
</p:sld>`, spChildren)
}

// paragraphsFromBody renders one <a:p> per body line with the theme body
// color/font. Used by content_chart and two_column.
func paragraphsFromBody(body []string, theme *Theme) string {
	if len(body) == 0 {
		return `<a:p><a:endParaRPr lang="en-US"/></a:p>`
	}
	var sb strings.Builder
	for _, line := range body {
		sb.WriteString("<a:p>")
		sb.WriteString(runText(line, 1600, false, theme.BodyColor, theme.BodyFont))
		sb.WriteString("</a:p>")
	}
	return sb.String()
}

// ── Title slide ─────────────────────────────────────────────────────────────
//
// Big centered title with optional subtitle (first body paragraph). A wide
// accent stripe sits below the title for visual weight.
func buildTitleSlide(s slideContent, theme *Theme) string {
	titleP := "<a:p><a:pPr algn=\"ctr\"/>" +
		runText(s.Title, 5400, true, theme.TitleColor, theme.TitleFont) +
		"</a:p>"

	subtitle := ""
	if len(s.Body) > 0 {
		subtitleP := "<a:p><a:pPr algn=\"ctr\"/>" +
			runText(s.Body[0], 2000, false, theme.MutedColor, theme.BodyFont) +
			"</a:p>"
		subtitle = textBox(3, "Subtitle", 914400, 3700000, slideW-2*914400, 600000, "t", subtitleP)
	}

	titleBox := textBox(2, "Title", 914400, 2400000, slideW-2*914400, 1100000, "ctr", titleP)
	stripe := rectShape(4, "Title Accent", (slideW-1800000)/2, 3500000, 1800000, 60000, theme.Accent1)

	return wrapSlide(titleBox + stripe + subtitle)
}

// ── Section divider ─────────────────────────────────────────────────────────
//
// Left-aligned large title, kicker text above, vertical accent bar on the
// far left. Used between major sections of a deck.
func buildSectionSlide(s slideContent, theme *Theme) string {
	bar := rectShape(2, "Section Bar", 0, 1800000, 120000, 3200000, theme.Accent1)

	kicker := ""
	if len(s.Body) > 0 {
		kickP := "<a:p>" + runText(strings.ToUpper(s.Body[0]), 1400, true, theme.Accent1, theme.BodyFont) + "</a:p>"
		kicker = textBox(3, "Kicker", 914400, 2200000, slideW-2*914400, 400000, "t", kickP)
	}

	titleP := "<a:p>" + runText(s.Title, 5000, true, theme.TitleColor, theme.TitleFont) + "</a:p>"
	titleBox := textBox(4, "Section Title", 914400, 2700000, slideW-2*914400, 1500000, "t", titleP)

	return wrapSlide(bar + kicker + titleBox)
}

// ── Two-column ──────────────────────────────────────────────────────────────
//
// Title across the top, two text columns of equal width below. Falls back
// to splitting Body in half if Col1/Col2 weren't populated by `:::col` divs.
func buildTwoColumnSlide(s slideContent, theme *Theme) string {
	col1 := s.Col1
	col2 := s.Col2
	if len(col1) == 0 && len(col2) == 0 && len(s.Body) > 0 {
		mid := (len(s.Body) + 1) / 2
		col1 = s.Body[:mid]
		col2 = s.Body[mid:]
	}

	titleP := "<a:p>" + runText(s.Title, 3200, true, theme.TitleColor, theme.TitleFont) + "</a:p>"
	titleBox := textBox(2, "Title", 457200, 274638, slideW-2*457200, 1143000, "b", titleP)

	const (
		colY     = 1700000
		colCY    = 4800000
		colMargX = 457200
		colGap   = 400000
	)
	colW := (slideW - 2*colMargX - colGap) / 2

	col1Box := textBox(3, "Column 1", colMargX, colY, colW, colCY, "t", paragraphsFromBody(col1, theme))
	col2Box := textBox(4, "Column 2", colMargX+colW+colGap, colY, colW, colCY, "t", paragraphsFromBody(col2, theme))

	return wrapSlide(titleBox + col1Box + col2Box)
}

// ── Content + chart (chart-emphasized) ──────────────────────────────────────
//
// Like the default content layout but with body text shrunk to a single
// caption line and the chart row taking ~70% of the slide height.
func buildContentChartSlide(s slideContent, chartIDBase int, theme *Theme) string {
	titleP := "<a:p>" + runText(s.Title, 3200, true, theme.TitleColor, theme.TitleFont) + "</a:p>"
	titleBox := textBox(2, "Title", 457200, 274638, slideW-2*457200, 1143000, "b", titleP)

	bodyXML := textBox(3, "Caption", 457200, 1500000, slideW-2*457200, 600000, "t", paragraphsFromBody(s.Body, theme))

	chartsXML := ""
	if len(s.Charts) > 0 {
		const (
			rowY    = 2200000
			rowCY   = 4400000
			marginX = 457200
			gutter  = 200000
		)
		rowWidth := slideW - 2*marginX
		n := len(s.Charts)
		chartCX := (rowWidth - gutter*(n-1)) / n
		nextID := 200
		for j := range s.Charts {
			x := marginX + j*(chartCX+gutter)
			rID := j + 2
			chartsXML += fmt.Sprintf(`<p:graphicFrame>
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

	return wrapSlide(titleBox + bodyXML + chartsXML)
}
