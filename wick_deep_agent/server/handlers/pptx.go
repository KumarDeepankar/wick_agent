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
	Title string
	Body  []string // paragraphs / bullet points
}

// parseMarkdownSlides splits markdown slide content into structured slides.
func parseMarkdownSlides(content string) []slideContent {
	// Strip <!-- slides --> marker
	content = regexp.MustCompile(`(?m)^\s*<!--\s*slides\s*-->\s*\n?`).ReplaceAllString(content, "")

	raw := strings.Split(content, "\n---\n")
	var slides []slideContent
	for _, block := range raw {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		s := slideContent{}
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

		// Collect body paragraphs
		var para strings.Builder
		inCode := false
		for _, line := range lines[bodyStart:] {
			trimmed := strings.TrimSpace(line)

			// Skip code/chart fences (just include the text content)
			if strings.HasPrefix(trimmed, "```") {
				if inCode {
					inCode = false
				} else {
					inCode = true
				}
				continue
			}
			if inCode {
				continue // skip code block contents for PPTX
			}

			// Skip sub-headings (include as body text)
			if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "## ") {
				if para.Len() > 0 {
					s.Body = append(s.Body, stripMarkdown(para.String()))
					para.Reset()
				}
				s.Body = append(s.Body, stripMarkdown(strings.TrimLeft(trimmed, "# ")))
				continue
			}

			// Bullet points
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
				if para.Len() > 0 {
					s.Body = append(s.Body, stripMarkdown(para.String()))
					para.Reset()
				}
				text := strings.TrimPrefix(trimmed, "- ")
				text = strings.TrimPrefix(text, "* ")
				s.Body = append(s.Body, "• "+stripMarkdown(text))
				continue
			}

			// Numbered list
			if matched, _ := regexp.MatchString(`^\d+\.\s`, trimmed); matched {
				if para.Len() > 0 {
					s.Body = append(s.Body, stripMarkdown(para.String()))
					para.Reset()
				}
				s.Body = append(s.Body, stripMarkdown(trimmed))
				continue
			}

			// Empty line = paragraph break
			if trimmed == "" {
				if para.Len() > 0 {
					s.Body = append(s.Body, stripMarkdown(para.String()))
					para.Reset()
				}
				continue
			}

			// Accumulate paragraph text
			if para.Len() > 0 {
				para.WriteString(" ")
			}
			para.WriteString(trimmed)
		}
		if para.Len() > 0 {
			s.Body = append(s.Body, stripMarkdown(para.String()))
		}

		if s.Title == "" && len(s.Body) > 0 {
			s.Title = s.Body[0]
			s.Body = s.Body[1:]
		}

		slides = append(slides, s)
	}
	return slides
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

// generatePPTX creates a minimal PPTX file from parsed slides.
func generatePPTX(slides []slideContent) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// EMU constants (English Metric Units: 1 inch = 914400 EMU)
	// Slide: 10" x 7.5"

	// [Content_Types].xml
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

	// Theme (minimal)
	writeZipFile(zw, "ppt/theme/theme1.xml", themeXML)

	// Slide master
	writeZipFile(zw, "ppt/slideMasters/slideMaster1.xml", slideMasterXML)
	writeZipFile(zw, "ppt/slideMasters/_rels/slideMaster1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/>
</Relationships>`)

	// Slide layout
	writeZipFile(zw, "ppt/slideLayouts/slideLayout1.xml", slideLayoutXML)
	writeZipFile(zw, "ppt/slideLayouts/_rels/slideLayout1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/>
</Relationships>`)

	// Slides
	for i, slide := range slides {
		writeZipFile(zw, fmt.Sprintf("ppt/slides/slide%d.xml", i+1), buildSlideXML(slide))
		writeZipFile(zw, fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>
</Relationships>`)
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

// buildSlideXML generates a single slide with title and body text.
// Uses standalone text boxes (not placeholder references) so shapes render
// without requiring matching placeholder definitions in the slide layout.
func buildSlideXML(s slideContent) string {
	titleEsc := html.EscapeString(s.Title)

	// Build body paragraphs
	var bodyParas string
	for _, para := range s.Body {
		bodyParas += fmt.Sprintf(`
            <a:p>
              <a:r><a:rPr lang="en-US" sz="1800" dirty="0"/><a:t>%s</a:t></a:r>
            </a:p>`, html.EscapeString(para))
	}
	if bodyParas == "" {
		bodyParas = `<a:p><a:endParaRPr lang="en-US"/></a:p>`
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
            <a:r><a:rPr lang="en-US" sz="3200" b="1" dirty="0"/><a:t>%s</a:t></a:r>
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
          <a:xfrm><a:off x="457200" y="1600200"/><a:ext cx="8229600" cy="4525963"/></a:xfrm>
          <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
          <a:noFill/>
        </p:spPr>
        <p:txBody>
          <a:bodyPr wrap="square" anchor="t"/>
          <a:lstStyle/>%s
        </p:txBody>
      </p:sp>
    </p:spTree>
  </p:cSld>
</p:sld>`, titleEsc, bodyParas)
}

// Minimal theme XML
const themeXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Wick Theme">
  <a:themeElements>
    <a:clrScheme name="Wick">
      <a:dk1><a:srgbClr val="1A1A2E"/></a:dk1>
      <a:lt1><a:srgbClr val="FFFFFF"/></a:lt1>
      <a:dk2><a:srgbClr val="333333"/></a:dk2>
      <a:lt2><a:srgbClr val="F5F5F5"/></a:lt2>
      <a:accent1><a:srgbClr val="6C5CE7"/></a:accent1>
      <a:accent2><a:srgbClr val="00B894"/></a:accent2>
      <a:accent3><a:srgbClr val="FDCB6E"/></a:accent3>
      <a:accent4><a:srgbClr val="E17055"/></a:accent4>
      <a:accent5><a:srgbClr val="74B9FF"/></a:accent5>
      <a:accent6><a:srgbClr val="A29BFE"/></a:accent6>
      <a:hlink><a:srgbClr val="6C5CE7"/></a:hlink>
      <a:folHlink><a:srgbClr val="A29BFE"/></a:folHlink>
    </a:clrScheme>
    <a:fontScheme name="Wick">
      <a:majorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont>
      <a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont>
    </a:fontScheme>
    <a:fmtScheme name="Wick">
      <a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst>
      <a:lnStyleLst><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst>
      <a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>
      <a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst>
    </a:fmtScheme>
  </a:themeElements>
</a:theme>`

// Minimal slide master
const slideMasterXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
  xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
  xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:cSld>
    <p:bg><p:bgPr><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill><a:effectLst/></p:bgPr></p:bg>
    <p:spTree>
      <p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>
      <p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>
    </p:spTree>
  </p:cSld>
  <p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>
  <p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>
</p:sldMaster>`

// Minimal slide layout
const slideLayoutXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
  xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
  xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
  type="obj">
  <p:cSld name="Title and Content">
    <p:spTree>
      <p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>
      <p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>
    </p:spTree>
  </p:cSld>
  <p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr>
</p:sldLayout>`
