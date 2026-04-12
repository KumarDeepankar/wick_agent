// Slides themes — TypeScript mirror of wick_deep_agent/server/handlers/themes/*.json.
//
// The Go server is the source of truth for the exported PPTX. This file
// duplicates the same palette in TS so the live preview in SlidesViewer
// matches what the user will get when they click Export PPTX. Whenever a
// theme JSON is added or changed on the Go side, mirror it here.

export interface SlidesTheme {
  name: string;
  displayName: string;
  bgColor: string;        // hex without '#'
  titleColor: string;
  bodyColor: string;
  mutedColor: string;
  accent1: string;
  accent2: string;
  titleFont: string;
  bodyFont: string;
  chartColors: string[];  // hex without '#'
}

export const SLIDES_THEMES: Record<string, SlidesTheme> = {
  corporate: {
    name: 'corporate',
    displayName: 'Corporate',
    bgColor: 'FFFFFF',
    titleColor: '0F172A',
    bodyColor: '334155',
    mutedColor: '94A3B8',
    accent1: '1E40AF',
    accent2: '0EA5A3',
    titleFont: 'Georgia, serif',
    bodyFont: '"Inter", -apple-system, BlinkMacSystemFont, sans-serif',
    chartColors: ['1E40AF', '0EA5A3', '0891B2', '7C3AED', 'DB2777', '059669', 'EA580C', '475569'],
  },
  editorial: {
    name: 'editorial',
    displayName: 'Editorial',
    bgColor: 'FAF7F0',
    titleColor: '1C1917',
    bodyColor: '44403C',
    mutedColor: 'A8A29E',
    accent1: 'B91C1C',
    accent2: '92400E',
    titleFont: 'Georgia, serif',
    bodyFont: 'Georgia, serif',
    chartColors: ['B91C1C', '92400E', '78350F', '065F46', '1E3A8A', '5B21B6', '9F1239', '374151'],
  },
  dark: {
    name: 'dark',
    displayName: 'Dark',
    bgColor: '0B0D10',
    titleColor: 'F1F5F9',
    bodyColor: 'CBD5E1',
    mutedColor: '64748B',
    accent1: '2DD4BF',
    accent2: 'A78BFA',
    titleFont: '"Inter", -apple-system, sans-serif',
    bodyFont: '"Inter", -apple-system, sans-serif',
    chartColors: ['2DD4BF', 'A78BFA', 'F59E0B', 'F87171', '60A5FA', '34D399', 'F472B6', '818CF8'],
  },
  vibrant: {
    name: 'vibrant',
    displayName: 'Vibrant',
    bgColor: 'FFFFFF',
    titleColor: '111827',
    bodyColor: '1F2937',
    mutedColor: '9CA3AF',
    accent1: 'EC4899',
    accent2: 'F97316',
    titleFont: '"Inter", -apple-system, sans-serif',
    bodyFont: '"Inter", -apple-system, sans-serif',
    chartColors: ['EC4899', 'F97316', '8B5CF6', '06B6D4', '10B981', 'F59E0B', 'EF4444', '3B82F6'],
  },
};

export const DEFAULT_THEME_NAME = 'corporate';

export function resolveSlidesTheme(name: string | undefined): SlidesTheme {
  if (name && SLIDES_THEMES[name]) {
    return SLIDES_THEMES[name];
  }
  return SLIDES_THEMES[DEFAULT_THEME_NAME]!;
}

/**
 * Returns CSS custom properties to apply to a slide stage wrapper. The
 * SlidesViewer wraps the rendered slide in a div with these vars and the
 * stage CSS reads them so theme switching is just a re-render.
 */
export function themeCssVars(theme: SlidesTheme): React.CSSProperties {
  return {
    ['--slides-bg' as string]: `#${theme.bgColor}`,
    ['--slides-title' as string]: `#${theme.titleColor}`,
    ['--slides-body' as string]: `#${theme.bodyColor}`,
    ['--slides-muted' as string]: `#${theme.mutedColor}`,
    ['--slides-accent1' as string]: `#${theme.accent1}`,
    ['--slides-accent2' as string]: `#${theme.accent2}`,
    ['--slides-title-font' as string]: theme.titleFont,
    ['--slides-body-font' as string]: theme.bodyFont,
  };
}
