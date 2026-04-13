package handlers

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// TableData is a parsed markdown pipe-table: a single header row plus any
// number of body rows. Column count is inferred from the header; missing
// cells in body rows are padded to match.
type TableData struct {
	Header []string
	Rows   [][]string
}

// tableSeparatorLine matches the `|---|:---:|---|` line that follows a
// header row in a GitHub-flavored markdown pipe table. Cells may use
// `:` alignment markers, which we currently ignore (all cells left-aligned).
var tableSeparatorLine = regexp.MustCompile(`^\s*\|(?:\s*:?-+:?\s*\|)+\s*$`)

// isTableHeader reports whether lines[i] + lines[i+1] look like a markdown
// pipe-table header+separator pair. Caller uses this for lookahead during
// slide body parsing.
func isTableHeader(lines []string, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	first := strings.TrimSpace(lines[i])
	if !strings.HasPrefix(first, "|") || !strings.HasSuffix(first, "|") {
		return false
	}
	return tableSeparatorLine.MatchString(lines[i+1])
}

// parseTableRow splits a `| a | b | c |` line into its cell values, stripping
// markdown formatting from each cell.
func parseTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, stripMarkdown(strings.TrimSpace(p)))
	}
	return out
}

// parseTableBlock consumes a contiguous table block starting at lines[i]
// (which must be a header — callers should gate on isTableHeader first)
// and returns the parsed table plus the index of the next unconsumed line.
func parseTableBlock(lines []string, i int) (*TableData, int) {
	header := parseTableRow(lines[i])
	next := i + 2 // skip header + separator
	var rows [][]string
	for next < len(lines) {
		line := strings.TrimSpace(lines[next])
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			break
		}
		row := parseTableRow(lines[next])
		// Pad or trim to match the header width so rendering is simple.
		for len(row) < len(header) {
			row = append(row, "")
		}
		if len(row) > len(header) {
			row = row[:len(header)]
		}
		rows = append(rows, row)
		next++
	}
	return &TableData{Header: header, Rows: rows}, next
}

// buildTableFrame emits a <p:graphicFrame> containing an <a:tbl> for the
// given table at EMU position (x, y) with total extent (cx, cy). The header
// row uses accent1 background with white text; body cells use the theme
// body color with a subtle muted border.
func buildTableFrame(t *TableData, theme *Theme, id, x, y, cx, cy int) string {
	cols := len(t.Header)
	if cols == 0 {
		return ""
	}
	colW := cx / cols

	// Row heights: header a bit taller than body rows for visual weight.
	const (
		headerH = 420000
		bodyH   = 340000
	)
	// Compute an effective cy that matches actual content so the frame
	// doesn't leave awkward whitespace below the table.
	effCy := headerH + bodyH*len(t.Rows)
	if effCy > cy {
		effCy = cy
	}

	var gridCols strings.Builder
	for i := 0; i < cols; i++ {
		w := colW
		// Last column absorbs rounding remainder so the grid totals cx.
		if i == cols-1 {
			w = cx - colW*(cols-1)
		}
		gridCols.WriteString(fmt.Sprintf(`<a:gridCol w="%d"/>`, w))
	}

	// Header row: accent1 fill, white bold text.
	var headerCells strings.Builder
	for _, cell := range t.Header {
		headerCells.WriteString(fmt.Sprintf(`
          <a:tc>
            <a:txBody>
              <a:bodyPr lIns="91440" tIns="45720" rIns="91440" bIns="45720" anchor="ctr"/>
              <a:lstStyle/>
              <a:p>
                <a:pPr algn="l"/>
                <a:r><a:rPr lang="en-US" sz="1300" b="1" dirty="0"><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill><a:latin typeface="%s"/></a:rPr><a:t>%s</a:t></a:r>
              </a:p>
            </a:txBody>
            <a:tcPr anchor="ctr">
              <a:lnL w="12700"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:lnL>
              <a:lnR w="12700"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:lnR>
              <a:lnT w="12700"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:lnT>
              <a:lnB w="12700"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:lnB>
              <a:solidFill><a:srgbClr val="%s"/></a:solidFill>
            </a:tcPr>
          </a:tc>`,
			theme.BodyFont, html.EscapeString(cell),
			theme.Accent1, theme.Accent1, theme.Accent1, theme.Accent1,
			theme.Accent1))
	}
	headerRow := fmt.Sprintf(`<a:tr h="%d">%s</a:tr>`, headerH, headerCells.String())

	// Body rows: theme body text, muted border. Rounded corners would need
	// a tableStyleId — keeping borders simple for v1.
	var bodyRows strings.Builder
	for _, row := range t.Rows {
		var cells strings.Builder
		for _, cell := range row {
			cells.WriteString(fmt.Sprintf(`
          <a:tc>
            <a:txBody>
              <a:bodyPr lIns="91440" tIns="45720" rIns="91440" bIns="45720" anchor="ctr"/>
              <a:lstStyle/>
              <a:p>
                <a:pPr algn="l"/>
                <a:r><a:rPr lang="en-US" sz="1200" dirty="0"><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:latin typeface="%s"/></a:rPr><a:t>%s</a:t></a:r>
              </a:p>
            </a:txBody>
            <a:tcPr anchor="ctr">
              <a:lnL w="6350"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:lnL>
              <a:lnR w="6350"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:lnR>
              <a:lnT w="6350"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:lnT>
              <a:lnB w="6350"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:lnB>
            </a:tcPr>
          </a:tc>`,
				theme.BodyColor, theme.BodyFont, html.EscapeString(cell),
				theme.MutedColor, theme.MutedColor, theme.MutedColor, theme.MutedColor))
		}
		bodyRows.WriteString(fmt.Sprintf(`<a:tr h="%d">%s</a:tr>`, bodyH, cells.String()))
	}

	return fmt.Sprintf(`<p:graphicFrame>
        <p:nvGraphicFramePr>
          <p:cNvPr id="%d" name="Table %d"/>
          <p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr>
          <p:nvPr/>
        </p:nvGraphicFramePr>
        <p:xfrm>
          <a:off x="%d" y="%d"/>
          <a:ext cx="%d" cy="%d"/>
        </p:xfrm>
        <a:graphic>
          <a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table">
            <a:tbl>
              <a:tblPr firstRow="1" bandRow="0"/>
              <a:tblGrid>%s</a:tblGrid>
              %s
              %s
            </a:tbl>
          </a:graphicData>
        </a:graphic>
      </p:graphicFrame>`,
		id, id, x, y, cx, effCy, gridCols.String(), headerRow, bodyRows.String())
}
