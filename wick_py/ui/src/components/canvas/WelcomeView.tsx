import { useState, useEffect } from 'react';
import type React from 'react';
import type { SkillInfo } from '../../types';
import { fetchSkills } from '../../api';

interface Props {
  onPromptClick: (prompt: string) => void;
}

const ICON_MAP: Record<string, React.ReactNode> = {
  code: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="16 18 22 12 16 6" />
      <polyline points="8 6 2 12 8 18" />
    </svg>
  ),
  table: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="18" height="18" rx="2" />
      <line x1="3" y1="9" x2="21" y2="9" />
      <line x1="9" y1="3" x2="9" y2="21" />
    </svg>
  ),
  search: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="11" cy="11" r="8" />
      <line x1="21" y1="21" x2="16.65" y2="16.65" />
    </svg>
  ),
  'bar-chart': (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="18" y1="20" x2="18" y2="10" />
      <line x1="12" y1="20" x2="12" y2="4" />
      <line x1="6" y1="20" x2="6" y2="14" />
    </svg>
  ),
  document: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <polyline points="14 2 14 8 20 8" />
    </svg>
  ),
  slides: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2" y="3" width="20" height="14" rx="2" />
      <line x1="8" y1="21" x2="16" y2="21" />
      <line x1="12" y1="17" x2="12" y2="21" />
    </svg>
  ),
};

const DEFAULT_ICON = (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
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

  if (loading) {
    return (
      <div className="welcome-skills">
        <div className="welcome-loading">
          <span className="welcome-spinner" />
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="welcome-skills">
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
          Retry loading skills
        </button>
      </div>
    );
  }

  return (
    <div className="welcome-skills">
      <div className="welcome-skill-chips">
        {skills.map((skill) => (
          <button
            key={skill.name}
            className="skill-chip"
            onClick={() => skill.samplePrompts[0] && onPromptClick(skill.samplePrompts[0])}
            title={skill.description}
          >
            <span className="skill-chip-icon">
              {ICON_MAP[skill.icon] ?? DEFAULT_ICON}
            </span>
            {formatSkillName(skill.name)}
          </button>
        ))}
      </div>
    </div>
  );
}
