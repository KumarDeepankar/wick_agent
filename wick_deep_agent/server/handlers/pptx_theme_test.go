package handlers

import (
	"strings"
	"testing"
)

func TestThemesLoadFromEmbeddedFS(t *testing.T) {
	want := []string{"corporate", "editorial", "dark", "vibrant"}
	got := AvailableThemes()
	have := make(map[string]bool, len(got))
	for _, name := range got {
		have[name] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("theme %q not loaded; got %v", name, got)
		}
	}
}

func TestResolveThemeFallsBackToDefault(t *testing.T) {
	t1 := ResolveTheme("nonexistent-theme-xyz")
	if t1 == nil || t1.Name != "corporate" {
		t.Errorf("unknown theme should fall back to corporate, got %+v", t1)
	}
	t2 := ResolveTheme("")
	if t2 == nil || t2.Name != "corporate" {
		t.Errorf("empty theme should fall back to corporate, got %+v", t2)
	}
}

func TestThemeDirectiveDrivesGeneratedPPTX(t *testing.T) {
	md := `<!-- theme: dark -->
# Dark Slide

` + "```chart" + `
type: bar
labels: [A, B]
data: [1, 2]
` + "```" + `
`
	deck := parseMarkdownSlides(md)
	if deck.Theme != "dark" {
		t.Fatalf("expected deck.Theme=dark, got %q", deck.Theme)
	}
	data, err := generatePPTX(deck)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)

	// Slide master should carry the dark theme background and accent colors.
	master := parts["ppt/slideMasters/slideMaster1.xml"]
	if !strings.Contains(master, "0B0D10") {
		t.Errorf("master missing dark bg color 0B0D10")
	}
	if !strings.Contains(master, "2DD4BF") {
		t.Errorf("master missing dark accent stripe color 2DD4BF")
	}

	// Title color from theme should appear in slide1.
	slide := parts["ppt/slides/slide1.xml"]
	if !strings.Contains(slide, "F1F5F9") {
		t.Errorf("slide1 missing dark title color F1F5F9")
	}

	// Chart series should be colored from the dark theme palette, NOT the
	// hardcoded fallback (2563EB).
	chart := parts["ppt/charts/chart1.xml"]
	if strings.Contains(chart, "2563EB") {
		t.Errorf("chart should not use hardcoded fallback color 2563EB under a theme")
	}
	if !strings.Contains(chart, "2DD4BF") {
		t.Errorf("chart should use first dark theme color 2DD4BF; got: %s", chart)
	}
}

func TestNoThemeDirectiveUsesDefault(t *testing.T) {
	md := `# Plain Slide

Body text only.
`
	deck := parseMarkdownSlides(md)
	if deck.Theme != "" {
		t.Errorf("expected empty deck.Theme, got %q", deck.Theme)
	}
	data, err := generatePPTX(deck)
	if err != nil {
		t.Fatalf("generatePPTX: %v", err)
	}
	parts := unzipParts(t, data)
	master := parts["ppt/slideMasters/slideMaster1.xml"]
	// Default = corporate, bg = white, accent = 1E40AF
	if !strings.Contains(master, "1E40AF") {
		t.Errorf("default theme should be corporate; missing accent 1E40AF")
	}
}
