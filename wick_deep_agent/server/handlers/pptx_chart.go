package handlers

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"strconv"
	"strings"
)

// ChartSeries is one named data series within a chart.
type ChartSeries struct {
	Name string
	Data []float64
}

// ChartConfig mirrors the ```chart fenced-block DSL parsed by the frontend
// at wick_py/ui/src/utils/chartRenderer.ts. Keep field names aligned with
// that file so the same markdown round-trips identically.
type ChartConfig struct {
	Type   string // bar, hbar, line, area, pie, donut, stacked_bar
	Title  string
	Labels []string
	Data   []float64
	Series []ChartSeries
	XLabel string
	YLabel string
	Colors []string
}

// parseChartDSL is the Go port of parseChartDSL() in chartRenderer.ts.
func parseChartDSL(text string) *ChartConfig {
	cfg := &ChartConfig{}
	lines := strings.Split(text, "\n")
	var current *ChartSeries
	inSeries := false

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "- name:") {
			inSeries = true
			if current != nil {
				cfg.Series = append(cfg.Series, *current)
			}
			current = &ChartSeries{Name: strings.TrimSpace(line[len("- name:"):])}
			continue
		}

		if inSeries && current != nil && strings.HasPrefix(line, "data:") {
			current.Data = parseNumberArray(strings.TrimSpace(line[len("data:"):]))
			continue
		}

		if line == "series:" {
			inSeries = true
			continue
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:colonIdx])
		if key != "data" || !inSeries || current == nil {
			if key != "series" {
				inSeries = false
			}
		}
		val := strings.TrimSpace(line[colonIdx+1:])

		switch key {
		case "type":
			cfg.Type = val
		case "title":
			cfg.Title = val
		case "xLabel":
			cfg.XLabel = val
		case "yLabel":
			cfg.YLabel = val
		case "labels":
			cfg.Labels = parseStringArray(val)
		case "colors":
			cfg.Colors = parseStringArray(val)
		case "data":
			if !inSeries {
				cfg.Data = parseNumberArray(val)
			}
		}
	}
	if current != nil {
		cfg.Series = append(cfg.Series, *current)
	}

	if len(cfg.Series) == 0 && len(cfg.Data) > 0 {
		cfg.Series = []ChartSeries{{Name: "Series 1", Data: cfg.Data}}
	}
	return cfg
}

func parseStringArray(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func parseNumberArray(v string) []float64 {
	parts := parseStringArray(v)
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		f, _ := strconv.ParseFloat(p, 64)
		out = append(out, f)
	}
	return out
}

var defaultChartColors = []string{
	"2563EB", "059669", "D97706", "DC2626",
	"7C3AED", "0D9488", "F59E0B", "6366F1",
}

func chartColorAt(colors []string, i int) string {
	if i < len(colors) {
		c := strings.TrimPrefix(strings.TrimSpace(colors[i]), "#")
		if c != "" {
			return strings.ToUpper(c)
		}
	}
	return defaultChartColors[i%len(defaultChartColors)]
}

func chartTypeNormalized(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

// chartTypeIsNative reports whether buildChartXML can emit a real DrawingML
// chart for this type.
func chartTypeIsNative(t string) bool {
	switch chartTypeNormalized(t) {
	case "bar", "hbar", "column", "line", "pie", "area", "donut", "stacked_bar", "":
		return true
	}
	return false
}

// buildChartXML returns the body of ppt/charts/chartN.xml for a supported
// chart type, including the externalData reference to the embedded workbook.
func buildChartXML(c *ChartConfig) string {
	var inner string
	switch chartTypeNormalized(c.Type) {
	case "line":
		inner = lineChartElement(c)
	case "pie":
		inner = pieChartElement(c, false)
	case "donut":
		inner = pieChartElement(c, true)
	case "area":
		inner = areaChartElement(c)
	case "stacked_bar":
		inner = barChartElement(c, true)
	default:
		inner = barChartElement(c, false)
	}
	return wrapChartSpace(inner)
}

func wrapChartSpace(chartElement string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart"
              xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
              xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  %s
  <c:externalData r:id="rId1">
    <c:autoUpdate val="0"/>
  </c:externalData>
</c:chartSpace>`, chartElement)
}

func chartTitleXML(title string) string {
	if title == "" {
		return `<c:autoTitleDeleted val="1"/>`
	}
	return fmt.Sprintf(`<c:title>
      <c:tx><c:rich>
        <a:bodyPr rot="0" spcFirstLastPara="1" vertOverflow="ellipsis" wrap="square" anchor="ctr" anchorCtr="1"/>
        <a:lstStyle/>
        <a:p>
          <a:pPr><a:defRPr sz="1400" b="1"/></a:pPr>
          <a:r><a:rPr lang="en-US" sz="1400" b="1"/><a:t>%s</a:t></a:r>
        </a:p>
      </c:rich></c:tx>
      <c:overlay val="0"/>
    </c:title>
    <c:autoTitleDeleted val="0"/>`, html.EscapeString(title))
}

func buildCatRef(labels []string) string {
	var pts strings.Builder
	for i, l := range labels {
		pts.WriteString(fmt.Sprintf(`<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, i, html.EscapeString(l)))
	}
	return fmt.Sprintf(`<c:cat>
            <c:strRef>
              <c:f>Sheet1!$A$2:$A$%d</c:f>
              <c:strCache>
                <c:ptCount val="%d"/>
                %s
              </c:strCache>
            </c:strRef>
          </c:cat>`, len(labels)+1, len(labels), pts.String())
}

func buildValRef(data []float64, colLetter string) string {
	var pts strings.Builder
	for i, v := range data {
		pts.WriteString(fmt.Sprintf(`<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, i, strconv.FormatFloat(v, 'f', -1, 64)))
	}
	return fmt.Sprintf(`<c:val>
            <c:numRef>
              <c:f>Sheet1!$%s$2:$%s$%d</c:f>
              <c:numCache>
                <c:formatCode>General</c:formatCode>
                <c:ptCount val="%d"/>
                %s
              </c:numCache>
            </c:numRef>
          </c:val>`, colLetter, colLetter, len(data)+1, len(data), pts.String())
}

// colLetter maps series index 0,1,2,... to spreadsheet columns B,C,D,...
// (column A is reserved for category labels). Limited to 25 series.
func colLetter(i int) string {
	if i < 0 || i > 24 {
		return "B"
	}
	return string(rune('B' + i))
}

func barSeriesXML(c *ChartConfig) string {
	var sb strings.Builder
	for i, s := range c.Series {
		color := chartColorAt(c.Colors, i)
		sb.WriteString(fmt.Sprintf(`
        <c:ser>
          <c:idx val="%d"/>
          <c:order val="%d"/>
          <c:tx><c:v>%s</c:v></c:tx>
          <c:spPr><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:ln><a:noFill/></a:ln></c:spPr>
          %s
          %s
        </c:ser>`, i, i, html.EscapeString(s.Name), color,
			buildCatRef(c.Labels), buildValRef(s.Data, colLetter(i))))
	}
	return sb.String()
}

func barChartElement(c *ChartConfig, stacked bool) string {
	barDir := "col"
	if strings.EqualFold(c.Type, "hbar") {
		barDir = "bar"
	}
	grouping := "clustered"
	overlap := ""
	if stacked {
		grouping = "stacked"
		overlap = `<c:overlap val="100"/>`
	}

	return fmt.Sprintf(`<c:chart>
    %s
    <c:plotArea>
      <c:layout/>
      <c:barChart>
        <c:barDir val="%s"/>
        <c:grouping val="%s"/>
        <c:varyColors val="0"/>
        %s
        <c:gapWidth val="150"/>
        %s
        <c:axId val="111111111"/>
        <c:axId val="222222222"/>
      </c:barChart>
      %s
    </c:plotArea>
    <c:plotVisOnly val="1"/>
    <c:dispBlanksAs val="gap"/>
  </c:chart>`, chartTitleXML(c.Title), barDir, grouping, barSeriesXML(c), overlap, catValAxesXML())
}

func lineSeriesXML(c *ChartConfig) string {
	var sb strings.Builder
	for i, s := range c.Series {
		color := chartColorAt(c.Colors, i)
		sb.WriteString(fmt.Sprintf(`
        <c:ser>
          <c:idx val="%d"/>
          <c:order val="%d"/>
          <c:tx><c:v>%s</c:v></c:tx>
          <c:spPr><a:ln w="22225" cap="rnd"><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:round/></a:ln></c:spPr>
          <c:marker><c:symbol val="circle"/><c:size val="6"/><c:spPr><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:ln><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:ln></c:spPr></c:marker>
          %s
          %s
          <c:smooth val="0"/>
        </c:ser>`, i, i, html.EscapeString(s.Name), color, color, color,
			buildCatRef(c.Labels), buildValRef(s.Data, colLetter(i))))
	}
	return sb.String()
}

func lineChartElement(c *ChartConfig) string {
	return fmt.Sprintf(`<c:chart>
    %s
    <c:plotArea>
      <c:layout/>
      <c:lineChart>
        <c:grouping val="standard"/>
        <c:varyColors val="0"/>
        %s
        <c:marker val="1"/>
        <c:axId val="111111111"/>
        <c:axId val="222222222"/>
      </c:lineChart>
      %s
    </c:plotArea>
    <c:plotVisOnly val="1"/>
    <c:dispBlanksAs val="gap"/>
  </c:chart>`, chartTitleXML(c.Title), lineSeriesXML(c), catValAxesXML())
}

func areaSeriesXML(c *ChartConfig) string {
	var sb strings.Builder
	for i, s := range c.Series {
		color := chartColorAt(c.Colors, i)
		sb.WriteString(fmt.Sprintf(`
        <c:ser>
          <c:idx val="%d"/>
          <c:order val="%d"/>
          <c:tx><c:v>%s</c:v></c:tx>
          <c:spPr>
            <a:solidFill><a:srgbClr val="%s"><a:alpha val="60000"/></a:srgbClr></a:solidFill>
            <a:ln w="19050"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:ln>
          </c:spPr>
          %s
          %s
        </c:ser>`, i, i, html.EscapeString(s.Name), color, color,
			buildCatRef(c.Labels), buildValRef(s.Data, colLetter(i))))
	}
	return sb.String()
}

func areaChartElement(c *ChartConfig) string {
	return fmt.Sprintf(`<c:chart>
    %s
    <c:plotArea>
      <c:layout/>
      <c:areaChart>
        <c:grouping val="standard"/>
        <c:varyColors val="0"/>
        %s
        <c:axId val="111111111"/>
        <c:axId val="222222222"/>
      </c:areaChart>
      %s
    </c:plotArea>
    <c:plotVisOnly val="1"/>
    <c:dispBlanksAs val="gap"/>
  </c:chart>`, chartTitleXML(c.Title), areaSeriesXML(c), catValAxesXML())
}

// pieChartElement renders a pie or doughnut chart from the first series only.
// donut=true switches the OOXML element from <c:pieChart> to <c:doughnutChart>
// and adds <c:holeSize>.
func pieChartElement(c *ChartConfig, donut bool) string {
	if len(c.Series) == 0 {
		return barChartElement(c, false)
	}
	s := c.Series[0]

	var dPts strings.Builder
	for i := range s.Data {
		color := chartColorAt(c.Colors, i)
		dPts.WriteString(fmt.Sprintf(`
          <c:dPt>
            <c:idx val="%d"/>
            <c:bubble3D val="0"/>
            <c:spPr><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:ln w="12700"><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill></a:ln></c:spPr>
          </c:dPt>`, i, color))
	}

	tag := "pieChart"
	holeSize := ""
	if donut {
		tag = "doughnutChart"
		holeSize = `<c:firstSliceAng val="0"/><c:holeSize val="50"/>`
	} else {
		holeSize = `<c:firstSliceAng val="0"/>`
	}

	return fmt.Sprintf(`<c:chart>
    %s
    <c:plotArea>
      <c:layout/>
      <c:%s>
        <c:varyColors val="1"/>
        <c:ser>
          <c:idx val="0"/>
          <c:order val="0"/>
          <c:tx><c:v>%s</c:v></c:tx>
          %s
          %s
          %s
        </c:ser>
        %s
      </c:%s>
    </c:plotArea>
    <c:plotVisOnly val="1"/>
    <c:dispBlanksAs val="gap"/>
  </c:chart>`, chartTitleXML(c.Title), tag, html.EscapeString(s.Name), dPts.String(),
		buildCatRef(c.Labels), buildValRef(s.Data, "B"), holeSize, tag)
}

func catValAxesXML() string {
	return `<c:catAx>
        <c:axId val="111111111"/>
        <c:scaling><c:orientation val="minMax"/></c:scaling>
        <c:delete val="0"/>
        <c:axPos val="b"/>
        <c:crossAx val="222222222"/>
        <c:crosses val="autoZero"/>
        <c:auto val="1"/>
        <c:lblAlgn val="ctr"/>
        <c:lblOffset val="100"/>
        <c:noMultiLvlLbl val="0"/>
      </c:catAx>
      <c:valAx>
        <c:axId val="222222222"/>
        <c:scaling><c:orientation val="minMax"/></c:scaling>
        <c:delete val="0"/>
        <c:axPos val="l"/>
        <c:crossAx val="111111111"/>
        <c:crosses val="autoZero"/>
        <c:crossBetween val="between"/>
      </c:valAx>`
}

// ── Embedded XLSX ───────────────────────────────────────────────────────────
//
// PowerPoint can render a chart from <c:numCache>/<c:strCache> alone, but the
// "Edit Data" affordance only works when the chart references a real embedded
// workbook via <c:externalData>. buildEmbeddedXLSX produces a minimal valid
// .xlsx (zip) containing one sheet whose layout matches the f= ranges in the
// chart XML: column A holds category labels, columns B,C,D... hold each
// series. Inline strings (t="inlineStr") are used to avoid a separate
// sharedStrings part.

func buildEmbeddedXLSX(c *ChartConfig) ([]byte, error) {
	// Determine the data shape we want in the sheet.
	type sheetSeries struct {
		name string
		data []float64
	}
	var series []sheetSeries
	if chartTypeNormalized(c.Type) == "pie" || chartTypeNormalized(c.Type) == "donut" {
		if len(c.Series) > 0 {
			series = append(series, sheetSeries{c.Series[0].Name, c.Series[0].Data})
		}
	} else {
		for _, s := range c.Series {
			series = append(series, sheetSeries{s.Name, s.Data})
		}
	}
	if len(series) == 0 {
		series = []sheetSeries{{name: "Series 1", data: c.Data}}
	}

	rows := len(c.Labels)
	if rows == 0 && len(series) > 0 {
		rows = len(series[0].data)
	}
	cols := len(series) // not counting label column A

	lastCol := colLetter(cols - 1)
	if cols == 0 {
		lastCol = "A"
	}
	dimRef := fmt.Sprintf("A1:%s%d", lastCol, rows+1)

	// sheet1.xml
	var sd strings.Builder
	sd.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <dimension ref="` + dimRef + `"/>
  <sheetViews><sheetView workbookViewId="0"/></sheetViews>
  <sheetFormatPr defaultRowHeight="15"/>
  <sheetData>`)

	// Row 1: A1 blank, then series names in B1, C1...
	sd.WriteString(`<row r="1">`)
	sd.WriteString(`<c r="A1"/>`)
	for i, s := range series {
		col := colLetter(i)
		sd.WriteString(fmt.Sprintf(`<c r="%s1" t="inlineStr"><is><t>%s</t></is></c>`, col, html.EscapeString(s.name)))
	}
	sd.WriteString(`</row>`)

	// Row 2..rows+1: label in A, values in B,C,...
	for r := 0; r < rows; r++ {
		sd.WriteString(fmt.Sprintf(`<row r="%d">`, r+2))
		label := ""
		if r < len(c.Labels) {
			label = c.Labels[r]
		}
		sd.WriteString(fmt.Sprintf(`<c r="A%d" t="inlineStr"><is><t>%s</t></is></c>`, r+2, html.EscapeString(label)))
		for i, s := range series {
			col := colLetter(i)
			val := 0.0
			if r < len(s.data) {
				val = s.data[r]
			}
			sd.WriteString(fmt.Sprintf(`<c r="%s%d"><v>%s</v></c>`, col, r+2, strconv.FormatFloat(val, 'f', -1, 64)))
		}
		sd.WriteString(`</row>`)
	}
	sd.WriteString(`</sheetData></worksheet>`)
	sheet1 := sd.String()

	// Other parts (constant).
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
  <Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
</Types>`

	rootRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`

	workbook := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"
          xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
    <sheet name="Sheet1" sheetId="1" r:id="rId1"/>
  </sheets>
</workbook>`

	workbookRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts>
  <fills count="1"><fill><patternFill patternType="none"/></fill></fills>
  <borders count="1"><border/></borders>
  <cellStyleXfs count="1"><xf/></cellStyleXfs>
  <cellXfs count="1"><xf/></cellXfs>
</styleSheet>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct{ name, body string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rootRels},
		{"xl/workbook.xml", workbook},
		{"xl/_rels/workbook.xml.rels", workbookRels},
		{"xl/styles.xml", styles},
		{"xl/worksheets/sheet1.xml", sheet1},
	}
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
