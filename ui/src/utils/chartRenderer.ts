/**
 * Chart DSL parser + inline SVG renderer for slide decks.
 *
 * DSL format (inside ```chart fenced code blocks):
 *   type: bar
 *   title: Revenue by Quarter
 *   labels: [Q1, Q2, Q3, Q4]
 *   data: [100, 150, 200, 180]
 *   series:
 *     - name: 2024
 *       data: [100, 150, 200, 180]
 *   legend: true
 *   showValues: true
 *   xLabel: Quarter
 *   yLabel: Revenue ($K)
 *   colors: [#2563eb, #059669]
 *   legendPosition: top | bottom | right
 */

// ── Types ────────────────────────────────────────────────────────────────

export interface ChartSeries {
  name: string;
  data: number[];
}

export interface ChartConfig {
  type: 'bar' | 'hbar' | 'line' | 'area' | 'pie' | 'donut' | 'stacked_bar';
  title?: string;
  labels: string[];
  data?: number[];
  series?: ChartSeries[];
  legend?: boolean;
  legendPosition?: 'top' | 'bottom' | 'right';
  showValues?: boolean;
  xLabel?: string;
  yLabel?: string;
  colors?: string[];
}

const DEFAULT_COLORS: string[] = [
  '#2563eb', '#059669', '#d97706', '#dc2626',
  '#7c3aed', '#0d9488', '#f59e0b', '#6366f1',
];

// ── Safe array access helpers ────────────────────────────────────────────

function at<T>(arr: T[], i: number, fallback: T): T {
  return i >= 0 && i < arr.length ? (arr[i] as T) : fallback;
}

function labelAt(labels: string[], i: number): string {
  return at(labels, i, `${i}`);
}

function valAt(data: number[], i: number): number {
  return at(data, i, 0);
}

// ── DSL Parser ───────────────────────────────────────────────────────────

function parseBracketArray(value: string): string[] {
  const inner = value.replace(/^\[/, '').replace(/\]$/, '');
  if (!inner.trim()) return [];
  return inner.split(',').map((s) => s.trim());
}

function parseNumberArray(value: string): number[] {
  return parseBracketArray(value).map((s) => parseFloat(s) || 0);
}

export function parseChartDSL(text: string): ChartConfig {
  const lines = text.split('\n');
  const cfg: Record<string, unknown> = {};
  let inSeries = false;
  let currentSeries: ChartSeries | null = null;
  const seriesList: ChartSeries[] = [];

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line) continue;

    // Series item start
    if (line.startsWith('- name:')) {
      inSeries = true;
      if (currentSeries) seriesList.push(currentSeries);
      currentSeries = { name: line.slice(7).trim(), data: [] };
      continue;
    }

    if (inSeries && currentSeries && line.startsWith('data:')) {
      currentSeries.data = parseNumberArray(line.slice(5).trim());
      continue;
    }

    // Top-level key:value
    if (line === 'series:') {
      inSeries = true;
      continue;
    }

    const colonIdx = line.indexOf(':');
    if (colonIdx <= 0) continue;

    // If we hit a non-series top-level key, close series context
    const key = line.slice(0, colonIdx).trim();
    if (key !== 'data' || !inSeries || !currentSeries) {
      if (key !== 'series') inSeries = false;
    }

    const val = line.slice(colonIdx + 1).trim();

    if (key === 'labels' || key === 'colors') {
      cfg[key] = parseBracketArray(val);
    } else if (key === 'data' && !inSeries) {
      cfg[key] = parseNumberArray(val);
    } else if (key === 'legend' || key === 'showValues') {
      cfg[key] = val === 'true';
    } else {
      cfg[key] = val;
    }
  }

  if (currentSeries) seriesList.push(currentSeries);

  const config: ChartConfig = {
    type: (cfg.type as ChartConfig['type']) || 'bar',
    labels: (cfg.labels as string[]) || [],
    title: cfg.title as string | undefined,
    legend: cfg.legend as boolean | undefined,
    legendPosition: cfg.legendPosition as ChartConfig['legendPosition'],
    showValues: cfg.showValues as boolean | undefined,
    xLabel: cfg.xLabel as string | undefined,
    yLabel: cfg.yLabel as string | undefined,
    colors: cfg.colors as string[] | undefined,
  };

  if (seriesList.length > 0) {
    config.series = seriesList;
  } else if (cfg.data) {
    config.data = cfg.data as number[];
  }

  return config;
}

// ── SVG Helpers ──────────────────────────────────────────────────────────

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function getColor(idx: number, colors?: string[]): string {
  const palette = colors && colors.length > 0 ? colors : DEFAULT_COLORS;
  return palette[idx % palette.length] ?? DEFAULT_COLORS[0] ?? '#2563eb';
}

function opacityAttr(label: string, activeFilter?: string | null): string {
  if (!activeFilter) return '';
  return label === activeFilter ? ' opacity="1"' : ' opacity="0.15"';
}

function highlightStroke(label: string, activeFilter?: string | null): string {
  if (!activeFilter || label !== activeFilter) return '';
  return ' stroke="#000" stroke-width="2"';
}

// ── Renderers ────────────────────────────────────────────────────────────

function getAllSeries(cfg: ChartConfig): ChartSeries[] {
  if (cfg.series && cfg.series.length > 0) return cfg.series;
  if (cfg.data) return [{ name: '', data: cfg.data }];
  return [];
}

function maxVal(cfg: ChartConfig): number {
  const all = getAllSeries(cfg);
  let mx = 0;
  if (cfg.type === 'stacked_bar') {
    for (let i = 0; i < cfg.labels.length; i++) {
      let sum = 0;
      for (const s of all) sum += valAt(s.data, i);
      mx = Math.max(mx, sum);
    }
  } else {
    for (const s of all) {
      for (const v of s.data) mx = Math.max(mx, v);
    }
  }
  return mx || 1;
}

function renderTitle(cfg: ChartConfig): string {
  if (!cfg.title) return '';
  return `<text x="300" y="24" text-anchor="middle" font-size="16" font-weight="700" fill="#111827">${esc(cfg.title)}</text>`;
}

function renderLegend(cfg: ChartConfig, series: ChartSeries[], x: number, y: number): string {
  if (!cfg.legend || series.length <= 1) return '';
  let html = '';
  const gap = 16;
  let cx = x;
  for (let i = 0; i < series.length; i++) {
    const s = series[i]!;
    const c = getColor(i, cfg.colors);
    html += `<rect x="${cx}" y="${y}" width="12" height="12" rx="2" fill="${c}"/>`;
    html += `<text x="${cx + 16}" y="${y + 10}" font-size="11" fill="#4b5563">${esc(s.name)}</text>`;
    cx += 16 + s.name.length * 7 + gap;
  }
  return html;
}

function renderAxisLabels(cfg: ChartConfig, plotLeft: number, plotBottom: number, plotWidth: number): string {
  let html = '';
  if (cfg.xLabel) {
    html += `<text x="${plotLeft + plotWidth / 2}" y="${plotBottom + 44}" text-anchor="middle" font-size="12" fill="#6b7280">${esc(cfg.xLabel)}</text>`;
  }
  if (cfg.yLabel) {
    html += `<text x="${plotLeft - 44}" y="${(plotBottom + 40) / 2}" text-anchor="middle" font-size="12" fill="#6b7280" transform="rotate(-90, ${plotLeft - 44}, ${(plotBottom + 40) / 2})">${esc(cfg.yLabel)}</text>`;
  }
  return html;
}

function renderGridLines(plotLeft: number, plotTop: number, plotWidth: number, plotHeight: number, mv: number, steps: number = 5): string {
  let html = '';
  for (let i = 0; i <= steps; i++) {
    const yy = plotTop + plotHeight - (i / steps) * plotHeight;
    const val = Math.round((i / steps) * mv);
    html += `<line x1="${plotLeft}" y1="${yy}" x2="${plotLeft + plotWidth}" y2="${yy}" stroke="#e5e7eb" stroke-width="1"/>`;
    html += `<text x="${plotLeft - 6}" y="${yy + 4}" text-anchor="end" font-size="10" fill="#9ca3af">${val}</text>`;
  }
  return html;
}

function renderBar(cfg: ChartConfig, activeFilter?: string | null): string {
  const series = getAllSeries(cfg);
  const n = cfg.labels.length;
  const nSeries = series.length;
  const mv = maxVal(cfg);

  const plotLeft = 60;
  const plotTop = cfg.title ? 40 : 16;
  const plotWidth = 500;
  const plotBottom = 340;
  const plotHeight = plotBottom - plotTop;
  const groupWidth = plotWidth / n;
  const barPad = nSeries > 1 ? 6 : 12;
  const barWidth = (groupWidth - barPad * 2) / nSeries;

  let svg = renderTitle(cfg);
  svg += renderGridLines(plotLeft, plotTop, plotWidth, plotHeight, mv);
  svg += `<line x1="${plotLeft}" y1="${plotBottom}" x2="${plotLeft + plotWidth}" y2="${plotBottom}" stroke="#d1d5db" stroke-width="1"/>`;

  for (let i = 0; i < n; i++) {
    const label = labelAt(cfg.labels, i);
    for (let si = 0; si < nSeries; si++) {
      const s = series[si]!;
      const val = valAt(s.data, i);
      const barH = (val / mv) * plotHeight;
      const bx = plotLeft + i * groupWidth + barPad + si * barWidth;
      const by = plotBottom - barH;
      const c = getColor(si, cfg.colors);
      svg += `<rect x="${bx}" y="${by}" width="${barWidth - 2}" height="${barH}" fill="${c}" rx="2" class="chart-clickable" data-label="${esc(label)}" style="cursor:pointer"${opacityAttr(label, activeFilter)}${highlightStroke(label, activeFilter)}/>`;
      if (cfg.showValues) {
        svg += `<text x="${bx + (barWidth - 2) / 2}" y="${by - 4}" text-anchor="middle" font-size="10" fill="#6b7280"${opacityAttr(label, activeFilter)}>${val}</text>`;
      }
    }
    svg += `<text x="${plotLeft + i * groupWidth + groupWidth / 2}" y="${plotBottom + 16}" text-anchor="middle" font-size="11" fill="#6b7280">${esc(label)}</text>`;
  }

  svg += renderAxisLabels(cfg, plotLeft, plotBottom, plotWidth);
  if (cfg.legend && nSeries > 1) {
    svg += renderLegend(cfg, series, plotLeft, plotBottom + 30);
  }

  return svg;
}

function renderHBar(cfg: ChartConfig, activeFilter?: string | null): string {
  const series = getAllSeries(cfg);
  const n = cfg.labels.length;
  const mv = maxVal(cfg);
  const nSeries = series.length;

  const plotLeft = 100;
  const plotTop = cfg.title ? 40 : 16;
  const plotWidth = 440;
  const plotBottom = 360;
  const plotHeight = plotBottom - plotTop;
  const groupHeight = plotHeight / n;
  const barPad = nSeries > 1 ? 4 : 8;
  const barH = (groupHeight - barPad * 2) / nSeries;

  let svg = renderTitle(cfg);
  svg += `<line x1="${plotLeft}" y1="${plotTop}" x2="${plotLeft}" y2="${plotBottom}" stroke="#d1d5db" stroke-width="1"/>`;

  for (let i = 0; i < n; i++) {
    const label = labelAt(cfg.labels, i);
    for (let si = 0; si < nSeries; si++) {
      const s = series[si]!;
      const val = valAt(s.data, i);
      const barW = (val / mv) * plotWidth;
      const by = plotTop + i * groupHeight + barPad + si * barH;
      const c = getColor(si, cfg.colors);
      svg += `<rect x="${plotLeft}" y="${by}" width="${barW}" height="${barH - 2}" fill="${c}" rx="2" class="chart-clickable" data-label="${esc(label)}" style="cursor:pointer"${opacityAttr(label, activeFilter)}${highlightStroke(label, activeFilter)}/>`;
      if (cfg.showValues) {
        svg += `<text x="${plotLeft + barW + 6}" y="${by + (barH - 2) / 2 + 4}" font-size="10" fill="#6b7280"${opacityAttr(label, activeFilter)}>${val}</text>`;
      }
    }
    svg += `<text x="${plotLeft - 8}" y="${plotTop + i * groupHeight + groupHeight / 2 + 4}" text-anchor="end" font-size="11" fill="#6b7280">${esc(label)}</text>`;
  }

  svg += renderAxisLabels(cfg, plotLeft, plotBottom, plotWidth);
  if (cfg.legend && nSeries > 1) {
    svg += renderLegend(cfg, series, plotLeft, plotBottom + 16);
  }

  return svg;
}

function renderLine(cfg: ChartConfig, activeFilter?: string | null): string {
  const series = getAllSeries(cfg);
  const n = cfg.labels.length;
  const mv = maxVal(cfg);

  const plotLeft = 60;
  const plotTop = cfg.title ? 40 : 16;
  const plotWidth = 500;
  const plotBottom = 340;
  const plotHeight = plotBottom - plotTop;

  let svg = renderTitle(cfg);
  svg += renderGridLines(plotLeft, plotTop, plotWidth, plotHeight, mv);
  svg += `<line x1="${plotLeft}" y1="${plotBottom}" x2="${plotLeft + plotWidth}" y2="${plotBottom}" stroke="#d1d5db" stroke-width="1"/>`;

  for (let si = 0; si < series.length; si++) {
    const s = series[si]!;
    const c = getColor(si, cfg.colors);
    const points: Array<[number, number]> = [];
    for (let i = 0; i < n; i++) {
      const val = valAt(s.data, i);
      const px = plotLeft + (i / Math.max(n - 1, 1)) * plotWidth;
      const py = plotBottom - (val / mv) * plotHeight;
      points.push([px, py]);
    }
    // Line path
    const pathD = points.map((p, idx) => (idx === 0 ? `M${p[0]},${p[1]}` : `L${p[0]},${p[1]}`)).join(' ');
    svg += `<path d="${pathD}" fill="none" stroke="${c}" stroke-width="2.5" stroke-linejoin="round"/>`;

    // Dots
    for (let i = 0; i < points.length; i++) {
      const pt = points[i]!;
      const label = labelAt(cfg.labels, i);
      svg += `<circle cx="${pt[0]}" cy="${pt[1]}" r="5" fill="${c}" class="chart-clickable" data-label="${esc(label)}" style="cursor:pointer"${opacityAttr(label, activeFilter)}${highlightStroke(label, activeFilter)}/>`;
      if (cfg.showValues) {
        svg += `<text x="${pt[0]}" y="${pt[1] - 10}" text-anchor="middle" font-size="10" fill="#6b7280"${opacityAttr(label, activeFilter)}>${valAt(s.data, i)}</text>`;
      }
    }
  }

  // X-axis labels
  for (let i = 0; i < n; i++) {
    const px = plotLeft + (i / Math.max(n - 1, 1)) * plotWidth;
    svg += `<text x="${px}" y="${plotBottom + 16}" text-anchor="middle" font-size="11" fill="#6b7280">${esc(labelAt(cfg.labels, i))}</text>`;
  }

  svg += renderAxisLabels(cfg, plotLeft, plotBottom, plotWidth);
  if (cfg.legend && series.length > 1) {
    svg += renderLegend(cfg, series, plotLeft, plotBottom + 30);
  }

  return svg;
}

function renderArea(cfg: ChartConfig, activeFilter?: string | null): string {
  const series = getAllSeries(cfg);
  const n = cfg.labels.length;
  const mv = maxVal(cfg);

  const plotLeft = 60;
  const plotTop = cfg.title ? 40 : 16;
  const plotWidth = 500;
  const plotBottom = 340;
  const plotHeight = plotBottom - plotTop;

  let svg = renderTitle(cfg);
  svg += renderGridLines(plotLeft, plotTop, plotWidth, plotHeight, mv);
  svg += `<line x1="${plotLeft}" y1="${plotBottom}" x2="${plotLeft + plotWidth}" y2="${plotBottom}" stroke="#d1d5db" stroke-width="1"/>`;

  for (let si = 0; si < series.length; si++) {
    const s = series[si]!;
    const c = getColor(si, cfg.colors);
    const points: Array<[number, number]> = [];
    for (let i = 0; i < n; i++) {
      const val = valAt(s.data, i);
      const px = plotLeft + (i / Math.max(n - 1, 1)) * plotWidth;
      const py = plotBottom - (val / mv) * plotHeight;
      points.push([px, py]);
    }
    // Fill area
    const lastPt = points[points.length - 1];
    const areaD = `M${plotLeft},${plotBottom} ` +
      points.map((p) => `L${p[0]},${p[1]}`).join(' ') +
      (lastPt ? ` L${lastPt[0]},${plotBottom} Z` : ' Z');
    svg += `<path d="${areaD}" fill="${c}" fill-opacity="0.15"/>`;
    // Line
    const lineD = points.map((p, idx) => (idx === 0 ? `M${p[0]},${p[1]}` : `L${p[0]},${p[1]}`)).join(' ');
    svg += `<path d="${lineD}" fill="none" stroke="${c}" stroke-width="2"/>`;

    // Dots
    for (let i = 0; i < points.length; i++) {
      const pt = points[i]!;
      const label = labelAt(cfg.labels, i);
      svg += `<circle cx="${pt[0]}" cy="${pt[1]}" r="4" fill="${c}" class="chart-clickable" data-label="${esc(label)}" style="cursor:pointer"${opacityAttr(label, activeFilter)}${highlightStroke(label, activeFilter)}/>`;
      if (cfg.showValues) {
        svg += `<text x="${pt[0]}" y="${pt[1] - 10}" text-anchor="middle" font-size="10" fill="#6b7280"${opacityAttr(label, activeFilter)}>${valAt(s.data, i)}</text>`;
      }
    }
  }

  for (let i = 0; i < n; i++) {
    const px = plotLeft + (i / Math.max(n - 1, 1)) * plotWidth;
    svg += `<text x="${px}" y="${plotBottom + 16}" text-anchor="middle" font-size="11" fill="#6b7280">${esc(labelAt(cfg.labels, i))}</text>`;
  }

  svg += renderAxisLabels(cfg, plotLeft, plotBottom, plotWidth);
  if (cfg.legend && series.length > 1) {
    svg += renderLegend(cfg, series, plotLeft, plotBottom + 30);
  }

  return svg;
}

function renderPieSlices(cfg: ChartConfig, cx: number, cy: number, r: number, innerR: number, activeFilter?: string | null): string {
  const data = cfg.data ?? cfg.series?.[0]?.data ?? [];
  const total = data.reduce((a, b) => a + b, 0) || 1;
  let svg = '';
  let startAngle = -Math.PI / 2;

  for (let i = 0; i < data.length; i++) {
    const label = labelAt(cfg.labels, i);
    const dv = valAt(data, i);
    const sliceAngle = (dv / total) * 2 * Math.PI;
    const endAngle = startAngle + sliceAngle;
    const largeArc = sliceAngle > Math.PI ? 1 : 0;

    const x1 = cx + r * Math.cos(startAngle);
    const y1 = cy + r * Math.sin(startAngle);
    const x2 = cx + r * Math.cos(endAngle);
    const y2 = cy + r * Math.sin(endAngle);

    let d: string;
    if (innerR > 0) {
      const ix1 = cx + innerR * Math.cos(startAngle);
      const iy1 = cy + innerR * Math.sin(startAngle);
      const ix2 = cx + innerR * Math.cos(endAngle);
      const iy2 = cy + innerR * Math.sin(endAngle);
      d = `M${ix1},${iy1} L${x1},${y1} A${r},${r} 0 ${largeArc},1 ${x2},${y2} L${ix2},${iy2} A${innerR},${innerR} 0 ${largeArc},0 ${ix1},${iy1} Z`;
    } else {
      d = `M${cx},${cy} L${x1},${y1} A${r},${r} 0 ${largeArc},1 ${x2},${y2} Z`;
    }

    const c = getColor(i, cfg.colors);
    svg += `<path d="${d}" fill="${c}" class="chart-clickable" data-label="${esc(label)}" style="cursor:pointer"${opacityAttr(label, activeFilter)}${highlightStroke(label, activeFilter)}/>`;

    // Label
    const midAngle = startAngle + sliceAngle / 2;
    const labelR = r + 18;
    const lx = cx + labelR * Math.cos(midAngle);
    const ly = cy + labelR * Math.sin(midAngle);
    const pct = Math.round((dv / total) * 100);
    const anchor = midAngle > Math.PI / 2 && midAngle < (3 * Math.PI) / 2 ? 'end' : 'start';
    const labelText = cfg.showValues ? `${label} (${pct}%)` : label;
    svg += `<text x="${lx}" y="${ly + 4}" text-anchor="${Math.abs(midAngle + Math.PI / 2) < 0.1 ? 'middle' : anchor}" font-size="11" fill="#4b5563"${opacityAttr(label, activeFilter)}>${esc(labelText)}</text>`;

    startAngle = endAngle;
  }

  return svg;
}

function renderPie(cfg: ChartConfig, activeFilter?: string | null): string {
  let svg = '';
  if (cfg.title) {
    svg += `<text x="200" y="24" text-anchor="middle" font-size="16" font-weight="700" fill="#111827">${esc(cfg.title)}</text>`;
  }
  svg += renderPieSlices(cfg, 200, 210, 130, 0, activeFilter);
  return svg;
}

function renderDonut(cfg: ChartConfig, activeFilter?: string | null): string {
  let svg = '';
  if (cfg.title) {
    svg += `<text x="200" y="24" text-anchor="middle" font-size="16" font-weight="700" fill="#111827">${esc(cfg.title)}</text>`;
  }
  svg += renderPieSlices(cfg, 200, 210, 130, 70, activeFilter);
  return svg;
}

function renderStackedBar(cfg: ChartConfig, activeFilter?: string | null): string {
  const series = getAllSeries(cfg);
  const n = cfg.labels.length;
  const mv = maxVal(cfg);

  const plotLeft = 60;
  const plotTop = cfg.title ? 40 : 16;
  const plotWidth = 500;
  const plotBottom = 340;
  const plotHeight = plotBottom - plotTop;
  const groupWidth = plotWidth / n;
  const barPad = 12;
  const barWidth = groupWidth - barPad * 2;

  let svg = renderTitle(cfg);
  svg += renderGridLines(plotLeft, plotTop, plotWidth, plotHeight, mv);
  svg += `<line x1="${plotLeft}" y1="${plotBottom}" x2="${plotLeft + plotWidth}" y2="${plotBottom}" stroke="#d1d5db" stroke-width="1"/>`;

  for (let i = 0; i < n; i++) {
    const label = labelAt(cfg.labels, i);
    let cumH = 0;
    for (let si = 0; si < series.length; si++) {
      const s = series[si]!;
      const val = valAt(s.data, i);
      const barH = (val / mv) * plotHeight;
      const bx = plotLeft + i * groupWidth + barPad;
      const by = plotBottom - cumH - barH;
      const c = getColor(si, cfg.colors);
      svg += `<rect x="${bx}" y="${by}" width="${barWidth}" height="${barH}" fill="${c}" class="chart-clickable" data-label="${esc(label)}" style="cursor:pointer"${opacityAttr(label, activeFilter)}${highlightStroke(label, activeFilter)}/>`;
      if (cfg.showValues && barH > 14) {
        svg += `<text x="${bx + barWidth / 2}" y="${by + barH / 2 + 4}" text-anchor="middle" font-size="10" fill="#fff"${opacityAttr(label, activeFilter)}>${val}</text>`;
      }
      cumH += barH;
    }
    svg += `<text x="${plotLeft + i * groupWidth + groupWidth / 2}" y="${plotBottom + 16}" text-anchor="middle" font-size="11" fill="#6b7280">${esc(label)}</text>`;
  }

  svg += renderAxisLabels(cfg, plotLeft, plotBottom, plotWidth);
  if (cfg.legend && series.length > 1) {
    svg += renderLegend(cfg, series, plotLeft, plotBottom + 30);
  }

  return svg;
}

// ── Main entry ───────────────────────────────────────────────────────────

export function renderChartSVG(text: string, chartIndex: number, activeFilter?: string | null): string {
  const cfg = parseChartDSL(text);
  const isPieType = cfg.type === 'pie' || cfg.type === 'donut';
  const viewBox = isPieType ? '0 0 400 400' : '0 0 600 400';
  const width = isPieType ? '400' : '600';
  const height = '400';

  let innerSvg = '';
  switch (cfg.type) {
    case 'bar':
      innerSvg = renderBar(cfg, activeFilter);
      break;
    case 'hbar':
      innerSvg = renderHBar(cfg, activeFilter);
      break;
    case 'line':
      innerSvg = renderLine(cfg, activeFilter);
      break;
    case 'area':
      innerSvg = renderArea(cfg, activeFilter);
      break;
    case 'pie':
      innerSvg = renderPie(cfg, activeFilter);
      break;
    case 'donut':
      innerSvg = renderDonut(cfg, activeFilter);
      break;
    case 'stacked_bar':
      innerSvg = renderStackedBar(cfg, activeFilter);
      break;
    default:
      innerSvg = renderBar(cfg, activeFilter);
  }

  return `<div class="chart-container" data-chart-id="chart-${chartIndex}"><svg viewBox="${viewBox}" width="${width}" height="${height}" xmlns="http://www.w3.org/2000/svg" style="max-width:100%;height:auto;font-family:Inter,-apple-system,BlinkMacSystemFont,sans-serif">${innerSvg}</svg></div>`;
}
