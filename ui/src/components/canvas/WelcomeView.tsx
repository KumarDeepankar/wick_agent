import type React from 'react';
import type { SkillInfo } from '../../types';

interface Props {
  onPromptClick: (prompt: string) => void;
}

const SKILLS: SkillInfo[] = [
  {
    name: 'Code Review',
    description: 'Analyze code files for quality, bugs, and best practices with actionable feedback.',
    samplePrompts: ['Review the code in /skills/csv-analyzer/analyze.py', 'Check /workspace/main.py for bugs'],
  },
  {
    name: 'CSV Analyzer',
    description: 'Parse and analyze CSV data files, generating summary statistics and visualizations.',
    samplePrompts: ['Analyze the CSV at /skills/csv-analyzer/sample_data.csv', 'Show statistics for /workspace/data.csv'],
  },
  {
    name: 'Research',
    description: 'Conduct in-depth research on topics, synthesizing information from multiple sources.',
    samplePrompts: ['Research best practices for Python async programming'],
  },
  {
    name: 'Data Analysis',
    description: 'Perform statistical analysis and generate insights from structured data sets.',
    samplePrompts: ['Analyze trends in the sales data', 'Calculate key metrics from /workspace/metrics.csv'],
  },
  {
    name: 'Summarization',
    description: 'Create concise summaries of long documents, reports, or code repositories.',
    samplePrompts: ['Summarize the project documentation', 'Give me an overview of this codebase'],
  },
];

const SKILL_ICONS: Record<string, React.ReactNode> = {
  'Code Review': (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="16 18 22 12 16 6" />
      <polyline points="8 6 2 12 8 18" />
    </svg>
  ),
  'CSV Analyzer': (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <line x1="3" y1="9" x2="21" y2="9" />
      <line x1="3" y1="15" x2="21" y2="15" />
      <line x1="9" y1="3" x2="9" y2="21" />
      <line x1="15" y1="3" x2="15" y2="21" />
    </svg>
  ),
  'Research': (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="11" cy="11" r="8" />
      <line x1="21" y1="21" x2="16.65" y2="16.65" />
    </svg>
  ),
  'Data Analysis': (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <line x1="18" y1="20" x2="18" y2="10" />
      <line x1="12" y1="20" x2="12" y2="4" />
      <line x1="6" y1="20" x2="6" y2="14" />
    </svg>
  ),
  'Summarization': (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <polyline points="14 2 14 8 20 8" />
      <line x1="16" y1="13" x2="8" y2="13" />
      <line x1="16" y1="17" x2="8" y2="17" />
      <polyline points="10 9 9 9 8 9" />
    </svg>
  ),
};

export function WelcomeView({ onPromptClick }: Props) {
  return (
    <div className="welcome-view">
      <div className="welcome-header">
        <h2 className="welcome-title">What can I help you with?</h2>
        <p className="welcome-subtitle">Choose a skill or type your own prompt</p>
      </div>
      <div className="welcome-grid">
        {SKILLS.map((skill) => (
          <div key={skill.name} className="skill-card">
            <div className="skill-card-icon">
              {SKILL_ICONS[skill.name]}
            </div>
            <h3 className="skill-card-name">{skill.name}</h3>
            <p className="skill-card-desc">{skill.description}</p>
            <div className="skill-card-prompts">
              {skill.samplePrompts.map((prompt) => (
                <button
                  key={prompt}
                  className="prompt-pill"
                  onClick={() => onPromptClick(prompt)}
                  title={prompt}
                >
                  {prompt.length > 45 ? prompt.slice(0, 42) + '...' : prompt}
                </button>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
