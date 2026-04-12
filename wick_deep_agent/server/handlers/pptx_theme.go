package handlers

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Theme is the contract every JSON file under handlers/themes/ must satisfy.
// All color values are hex strings without the leading #, uppercase preferred
// (we normalize on load so JSON authors don't have to remember).
type Theme struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`

	BgColor    string `json:"bg_color"`
	TitleColor string `json:"title_color"`
	BodyColor  string `json:"body_color"`
	MutedColor string `json:"muted_color"`

	Accent1 string `json:"accent1"`
	Accent2 string `json:"accent2"`

	TitleFont string `json:"title_font"`
	BodyFont  string `json:"body_font"`

	ChartColors []string `json:"chart_colors"`

	AccentStripe bool `json:"accent_stripe"`
	Footer       bool `json:"footer"`
}

const defaultThemeName = "corporate"

//go:embed themes/*.json
var themeFS embed.FS

var (
	themesOnce sync.Once
	themesMap  map[string]*Theme
)

func loadThemes() {
	themesOnce.Do(func() {
		themesMap = make(map[string]*Theme)
		entries, err := themeFS.ReadDir("themes")
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, err := themeFS.ReadFile("themes/" + e.Name())
			if err != nil {
				continue
			}
			var t Theme
			if err := json.Unmarshal(data, &t); err != nil {
				continue
			}
			normalize(&t)
			themesMap[t.Name] = &t
		}
	})
}

func normalize(t *Theme) {
	t.BgColor = upperHex(t.BgColor, "FFFFFF")
	t.TitleColor = upperHex(t.TitleColor, "0F172A")
	t.BodyColor = upperHex(t.BodyColor, "334155")
	t.MutedColor = upperHex(t.MutedColor, "94A3B8")
	t.Accent1 = upperHex(t.Accent1, "1E40AF")
	t.Accent2 = upperHex(t.Accent2, "0EA5A3")
	if t.TitleFont == "" {
		t.TitleFont = "Calibri"
	}
	if t.BodyFont == "" {
		t.BodyFont = "Calibri"
	}
	for i, c := range t.ChartColors {
		t.ChartColors[i] = upperHex(c, "1E40AF")
	}
	if len(t.ChartColors) == 0 {
		t.ChartColors = []string{"1E40AF", "0EA5A3", "DB2777", "059669", "EA580C", "7C3AED", "0891B2", "475569"}
	}
}

func upperHex(v, fallback string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "#")
	if v == "" {
		return fallback
	}
	return strings.ToUpper(v)
}

// ResolveTheme returns the named theme or the default if name is unknown
// or empty. Never returns nil — falls back to a hardcoded baseline theme
// if even the default JSON failed to load (should not happen at runtime
// since the JSONs are embedded).
func ResolveTheme(name string) *Theme {
	loadThemes()
	if t, ok := themesMap[strings.TrimSpace(name)]; ok {
		return t
	}
	if t, ok := themesMap[defaultThemeName]; ok {
		return t
	}
	t := &Theme{Name: "fallback"}
	normalize(t)
	return t
}

// AvailableThemes returns the names of all loaded themes (sorted is not
// guaranteed; callers can sort if needed). Useful for the agent prompt.
func AvailableThemes() []string {
	loadThemes()
	out := make([]string, 0, len(themesMap))
	for name := range themesMap {
		out = append(out, name)
	}
	return out
}

// ── Theme-aware OOXML builders ──────────────────────────────────────────────

// buildThemeXML emits ppt/theme/theme1.xml using the theme palette + fonts.
// The accent slots map: accent1 → primary, accent2 → secondary; remaining
// accents pull from the chart palette so picker swatches in PowerPoint match.
func buildThemeXML(t *Theme) string {
	cc := t.ChartColors
	get := func(i int, fallback string) string {
		if i < len(cc) {
			return cc[i]
		}
		return fallback
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="%s">
  <a:themeElements>
    <a:clrScheme name="%s">
      <a:dk1><a:srgbClr val="%s"/></a:dk1>
      <a:lt1><a:srgbClr val="%s"/></a:lt1>
      <a:dk2><a:srgbClr val="%s"/></a:dk2>
      <a:lt2><a:srgbClr val="%s"/></a:lt2>
      <a:accent1><a:srgbClr val="%s"/></a:accent1>
      <a:accent2><a:srgbClr val="%s"/></a:accent2>
      <a:accent3><a:srgbClr val="%s"/></a:accent3>
      <a:accent4><a:srgbClr val="%s"/></a:accent4>
      <a:accent5><a:srgbClr val="%s"/></a:accent5>
      <a:accent6><a:srgbClr val="%s"/></a:accent6>
      <a:hlink><a:srgbClr val="%s"/></a:hlink>
      <a:folHlink><a:srgbClr val="%s"/></a:folHlink>
    </a:clrScheme>
    <a:fontScheme name="%s">
      <a:majorFont><a:latin typeface="%s"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont>
      <a:minorFont><a:latin typeface="%s"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont>
    </a:fontScheme>
    <a:fmtScheme name="%s">
      <a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst>
      <a:lnStyleLst><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln><a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst>
      <a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>
      <a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst>
    </a:fmtScheme>
  </a:themeElements>
</a:theme>`,
		t.DisplayName, t.Name,
		t.TitleColor, t.BgColor, t.BodyColor, t.MutedColor,
		t.Accent1, t.Accent2,
		get(2, t.Accent1), get(3, t.Accent2), get(4, t.Accent1), get(5, t.Accent2),
		t.Accent1, t.Accent2,
		t.Name, t.TitleFont, t.BodyFont, t.Name)
}

// buildSlideMasterXML emits the slide master with theme background, accent
// stripe under the title region, and a footer slide-number field. Shapes on
// the master appear on every slide automatically — no per-slide repetition.
func buildSlideMasterXML(t *Theme) string {
	stripe := ""
	if t.AccentStripe {
		stripe = fmt.Sprintf(`
      <p:sp>
        <p:nvSpPr>
          <p:cNvPr id="10" name="Accent Stripe"/>
          <p:cNvSpPr/>
          <p:nvPr/>
        </p:nvSpPr>
        <p:spPr>
          <a:xfrm><a:off x="457200" y="1480000"/><a:ext cx="1200000" cy="60000"/></a:xfrm>
          <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
          <a:solidFill><a:srgbClr val="%s"/></a:solidFill>
          <a:ln><a:noFill/></a:ln>
        </p:spPr>
        <p:txBody>
          <a:bodyPr/><a:lstStyle/><a:p><a:endParaRPr lang="en-US"/></a:p>
        </p:txBody>
      </p:sp>`, t.Accent1)
	}

	footer := ""
	if t.Footer {
		footer = fmt.Sprintf(`
      <p:sp>
        <p:nvSpPr>
          <p:cNvPr id="11" name="Slide Number Placeholder"/>
          <p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>
          <p:nvPr><p:ph type="sldNum" sz="quarter"/></p:nvPr>
        </p:nvSpPr>
        <p:spPr>
          <a:xfrm><a:off x="8000000" y="6500000"/><a:ext cx="800000" cy="300000"/></a:xfrm>
          <a:prstGeom prst="rect"><a:avLst/></a:prstGeom>
        </p:spPr>
        <p:txBody>
          <a:bodyPr wrap="none" anchor="b"/>
          <a:lstStyle/>
          <a:p>
            <a:pPr algn="r"/>
            <a:fld id="{4F8B9C2D-1234-4321-A001-000000000001}" type="slidenum">
              <a:rPr lang="en-US" sz="1000"><a:solidFill><a:srgbClr val="%s"/></a:solidFill></a:rPr>
              <a:t>‹#›</a:t>
            </a:fld>
          </a:p>
        </p:txBody>
      </p:sp>`, t.MutedColor)
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
  xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
  xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:cSld>
    <p:bg><p:bgPr><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:effectLst/></p:bgPr></p:bg>
    <p:spTree>
      <p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>
      <p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>%s%s
    </p:spTree>
  </p:cSld>
  <p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>
  <p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst>
</p:sldMaster>`, t.BgColor, stripe, footer)
}

// buildSlideLayoutXML emits a minimal slide layout that inherits everything
// from the master. The actual title/body shapes live on the slide itself
// (standalone text boxes), so the layout body is intentionally empty.
func buildSlideLayoutXML(t *Theme) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
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
}
