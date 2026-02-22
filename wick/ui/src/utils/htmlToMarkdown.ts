import TurndownService from 'turndown';

let _instance: TurndownService | null = null;

function getInstance(): TurndownService {
  if (_instance) return _instance;

  const td = new TurndownService({
    headingStyle: 'atx',
    codeBlockStyle: 'fenced',
    bulletListMarker: '-',
    emDelimiter: '*',
    strongDelimiter: '**',
  });

  // Preserve chart blocks by reading the embedded DSL source
  td.addRule('chartContainer', {
    filter: (node) =>
      node.nodeName === 'DIV' &&
      (node as HTMLElement).classList.contains('chart-container') &&
      (node as HTMLElement).hasAttribute('data-chart-source'),
    replacement: (_content, node) => {
      const encoded = (node as HTMLElement).getAttribute('data-chart-source') ?? '';
      try {
        const src = decodeURIComponent(escape(atob(encoded)));
        return `\n\n\`\`\`chart\n${src}\n\`\`\`\n\n`;
      } catch {
        return '';
      }
    },
  });

  // Preserve fenced code blocks
  td.addRule('fencedCode', {
    filter: (node) => {
      return (
        node.nodeName === 'PRE' &&
        node.firstChild !== null &&
        (node.firstChild as HTMLElement).nodeName === 'CODE'
      );
    },
    replacement: (_content, node) => {
      const codeEl = (node as HTMLElement).querySelector('code');
      if (!codeEl) return _content;
      const lang =
        Array.from(codeEl.classList)
          .find((c) => c.startsWith('language-'))
          ?.replace('language-', '') ?? '';
      const code = codeEl.textContent ?? '';
      return `\n\n\`\`\`${lang}\n${code}\n\`\`\`\n\n`;
    },
  });

  _instance = td;
  return td;
}

export function htmlToMarkdown(html: string): string {
  return getInstance().turndown(html).trim();
}
