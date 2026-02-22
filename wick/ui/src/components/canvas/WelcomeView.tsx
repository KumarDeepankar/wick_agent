import { useState, useEffect } from 'react';
import type React from 'react';
import type { SkillInfo } from '../../types';
import { fetchSkills } from '../../api';

interface Props {
  onPromptClick: (prompt: string) => void;
}

const ICON_MAP: Record<string, React.ReactNode> = {
  code: (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="16 18 22 12 16 6" />
      <polyline points="8 6 2 12 8 18" />
    </svg>
  ),
  table: (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <line x1="3" y1="9" x2="21" y2="9" />
      <line x1="3" y1="15" x2="21" y2="15" />
      <line x1="9" y1="3" x2="9" y2="21" />
      <line x1="15" y1="3" x2="15" y2="21" />
    </svg>
  ),
  search: (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="11" cy="11" r="8" />
      <line x1="21" y1="21" x2="16.65" y2="16.65" />
    </svg>
  ),
  'bar-chart': (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <line x1="18" y1="20" x2="18" y2="10" />
      <line x1="12" y1="20" x2="12" y2="4" />
      <line x1="6" y1="20" x2="6" y2="14" />
    </svg>
  ),
  document: (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <polyline points="14 2 14 8 20 8" />
      <line x1="16" y1="13" x2="8" y2="13" />
      <line x1="16" y1="17" x2="8" y2="17" />
      <polyline points="10 9 9 9 8 9" />
    </svg>
  ),
  slides: (
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2" y="3" width="20" height="14" rx="2" />
      <line x1="8" y1="21" x2="16" y2="21" />
      <line x1="12" y1="17" x2="12" y2="21" />
    </svg>
  ),
};

const DEFAULT_ICON = (
  <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    <circle cx="12" cy="12" r="10" />
    <line x1="12" y1="16" x2="12" y2="12" />
    <line x1="12" y1="8" x2="12.01" y2="8" />
  </svg>
);

function formatSkillName(name: string): string {
  return name
    .split('-')
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ');
}

export function WelcomeView({ onPromptClick }: Props) {
  const [skills, setSkills] = useState<SkillInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  useEffect(() => {
    fetchSkills()
      .then((data) => {
        setSkills(data);
        setLoading(false);
      })
      .catch(() => {
        setError(true);
        setLoading(false);
      });
  }, []);

  return (
    <div className="welcome-view">
      <div className="welcome-header">
        <h2 className="welcome-title">What can I help you with?</h2>
        <p className="welcome-subtitle">Choose a skill or type your own prompt</p>
      </div>
      {loading && (
        <div className="welcome-loading">
          <span className="welcome-spinner" />
          Loading skills...
        </div>
      )}
      {error && (
        <div className="welcome-error">
          <span>Failed to load skills.</span>
          <button
            className="welcome-retry-btn"
            onClick={() => {
              setError(false);
              setLoading(true);
              fetchSkills()
                .then((data) => {
                  setSkills(data);
                  setLoading(false);
                })
                .catch(() => {
                  setError(true);
                  setLoading(false);
                });
            }}
          >
            Retry
          </button>
        </div>
      )}
      {!loading && !error && (
        <div className="welcome-grid">
          {skills.map((skill) => (
            <div key={skill.name} className="skill-card">
              <div className="skill-card-icon">
                {ICON_MAP[skill.icon] ?? DEFAULT_ICON}
              </div>
              <h3 className="skill-card-name">{formatSkillName(skill.name)}</h3>
              <p className="skill-card-desc">{skill.description}</p>
              {skill.samplePrompts.length > 0 && (
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
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
