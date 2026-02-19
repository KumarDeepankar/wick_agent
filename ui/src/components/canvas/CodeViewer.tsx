import { useEffect, useRef, useState } from 'react';
import hljs from 'highlight.js/lib/core';
import python from 'highlight.js/lib/languages/python';
import javascript from 'highlight.js/lib/languages/javascript';
import typescript from 'highlight.js/lib/languages/typescript';
import java from 'highlight.js/lib/languages/java';
import bash from 'highlight.js/lib/languages/bash';
import json from 'highlight.js/lib/languages/json';
import yaml from 'highlight.js/lib/languages/yaml';
import sql from 'highlight.js/lib/languages/sql';
import css from 'highlight.js/lib/languages/css';
import xml from 'highlight.js/lib/languages/xml';

hljs.registerLanguage('python', python);
hljs.registerLanguage('javascript', javascript);
hljs.registerLanguage('typescript', typescript);
hljs.registerLanguage('java', java);
hljs.registerLanguage('bash', bash);
hljs.registerLanguage('json', json);
hljs.registerLanguage('yaml', yaml);
hljs.registerLanguage('sql', sql);
hljs.registerLanguage('css', css);
hljs.registerLanguage('xml', xml);
hljs.registerLanguage('html', xml);

interface Props {
  content: string;
  language?: string;
  fileName: string;
}

export function CodeViewer({ content, language, fileName }: Props) {
  const codeRef = useRef<HTMLElement>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (codeRef.current) {
      codeRef.current.textContent = content;
      if (language && hljs.getLanguage(language)) {
        hljs.highlightElement(codeRef.current);
      } else {
        hljs.highlightElement(codeRef.current);
      }
    }
  }, [content, language]);

  const handleCopy = async () => {
    await navigator.clipboard.writeText(content);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const lineCount = content.split('\n').length;

  return (
    <div className="code-viewer">
      <div className="code-viewer-header">
        <span className="code-viewer-filename">{fileName}</span>
        <span className="code-viewer-meta">{language ?? 'text'} &middot; {lineCount} lines</span>
        <button className={`copy-btn ${copied ? 'copied' : ''}`} onClick={handleCopy}>
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <div className="code-viewer-body">
        <div className="code-line-numbers">
          {Array.from({ length: lineCount }, (_, i) => (
            <span key={i}>{i + 1}</span>
          ))}
        </div>
        <pre className="code-viewer-pre">
          <code ref={codeRef} className={language ? `language-${language}` : ''}>
            {content}
          </code>
        </pre>
      </div>
    </div>
  );
}
