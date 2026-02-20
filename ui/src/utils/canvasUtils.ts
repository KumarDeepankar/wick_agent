import type { CanvasContentType } from '../types';

const CODE_EXTENSIONS: Record<string, string> = {
  '.py': 'python',
  '.js': 'javascript',
  '.ts': 'typescript',
  '.tsx': 'typescript',
  '.jsx': 'javascript',
  '.java': 'java',
  '.go': 'go',
  '.rs': 'rust',
  '.sh': 'bash',
  '.bash': 'bash',
  '.zsh': 'bash',
  '.json': 'json',
  '.yaml': 'yaml',
  '.yml': 'yaml',
  '.xml': 'xml',
  '.html': 'html',
  '.css': 'css',
  '.sql': 'sql',
  '.rb': 'ruby',
  '.php': 'php',
  '.c': 'c',
  '.cpp': 'cpp',
  '.h': 'c',
  '.hpp': 'cpp',
  '.cs': 'csharp',
  '.swift': 'swift',
  '.kt': 'kotlin',
  '.r': 'r',
  '.toml': 'ini',
  '.ini': 'ini',
  '.env': 'bash',
  '.dockerfile': 'dockerfile',
  '.tf': 'plaintext',
};

const DATA_EXTENSIONS = new Set(['.csv', '.tsv']);
const DOCUMENT_EXTENSIONS = new Set(['.md', '.txt', '.rst', '.log']);
const SLIDES_EXTENSIONS = new Set(['.pptx']);
const BINARY_EXTENSIONS = new Set(['.xlsx', '.docx', '.pdf', '.zip', '.tar', '.gz', '.png', '.jpg', '.jpeg', '.gif', '.svg', '.ico', '.woff', '.woff2', '.ttf', '.eot', '.mp3', '.mp4', '.wav', '.avi']);

export function extractExtension(filePath: string): string {
  const dot = filePath.lastIndexOf('.');
  return dot >= 0 ? filePath.slice(dot).toLowerCase() : '';
}

export function extractFileName(filePath: string): string {
  const slash = filePath.lastIndexOf('/');
  return slash >= 0 ? filePath.slice(slash + 1) : filePath;
}

export function resolveContentType(ext: string): CanvasContentType {
  if (CODE_EXTENSIONS[ext]) return 'code';
  if (DATA_EXTENSIONS.has(ext)) return 'data';
  if (DOCUMENT_EXTENSIONS.has(ext)) return 'document';
  if (SLIDES_EXTENSIONS.has(ext)) return 'slides';
  if (BINARY_EXTENSIONS.has(ext)) return 'binary';
  // Default unknown text files to code
  return 'code';
}

export function resolveLanguage(ext: string): string | undefined {
  return CODE_EXTENSIONS[ext];
}

export function isBinaryExtension(ext: string): boolean {
  return BINARY_EXTENSIONS.has(ext);
}

/** Extensions hidden from display names (technical file types). */
const HIDDEN_EXTENSIONS = new Set(['.md', '.txt', '.rst', '.log']);

/**
 * User-friendly display name: strips technical extensions (.md, .txt, etc.)
 * and converts kebab/snake-case to title case.
 */
export function getDisplayName(fileName: string, contentType: CanvasContentType): string {
  let name = fileName;
  const ext = extractExtension(fileName);

  // Strip technical extensions for non-code content
  if (contentType !== 'code' && HIDDEN_EXTENSIONS.has(ext)) {
    name = name.replace(/\.[^.]+$/, '');
  }

  // Convert kebab-case / snake_case / dots to spaces and title-case
  name = name
    .replace(/[-_\.]/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase())
    .trim();

  return name || fileName;
}

const FORMAT_LABELS: Record<CanvasContentType, string> = {
  slides: 'Presentation',
  document: 'Document',
  data: 'Spreadsheet',
  code: 'Code',
  binary: 'File',
  welcome: '',
};

/**
 * Short user-friendly format label for the content type.
 */
export function getFormatLabel(contentType: CanvasContentType): string {
  return FORMAT_LABELS[contentType] || '';
}

/**
 * Detect if markdown content looks like a slide deck.
 *
 * Returns true if:
 *  - Content starts with `<!-- slides -->` marker (any number of slides), OR
 *  - Content has 1+ `\n---\n` separators and starts with a `#` heading
 */
export function isSlideContent(content: string): boolean {
  const trimmed = content.trimStart();
  // Explicit marker — always treat as slides regardless of separator count
  if (trimmed.startsWith('<!-- slides -->')) return true;
  // Heuristic — at least one separator + heading on first line
  const separatorCount = (content.match(/\n---\n/g) || []).length;
  if (separatorCount < 1) return false;
  const firstLine = trimmed.split('\n')[0]?.trim() ?? '';
  return firstLine.startsWith('#');
}
