import { useState, useRef, useEffect, useCallback, useMemo, type KeyboardEvent, type CSSProperties } from 'react';
import { useTheme } from '../contexts/ThemeContext';
import { useStreamingSearch } from '../hooks/useStreamingSearch';
import { useChatContext } from '../contexts/ChatContext';
import { apiClient } from '../services/api';
import { getBackendUrl } from '../config';
import { Icon } from './Icon';
import { TRANSITION } from '../styles/animations';
import { WILL_CHANGE, FLEX } from '../styles/styleUtils';
import type { SearchMode } from '../types';

// =============================================================================
// STATIC STYLES - Defined outside component to prevent recreation
// =============================================================================

const STYLES = {
  container: {
    backgroundColor: 'transparent',
    padding: 0,
    marginLeft: '0',
    marginRight: '0',
    marginBottom: '8px',
    width: '100%',
    boxSizing: 'border-box',
  } as CSSProperties,
  brandingText: {
    textAlign: 'center',
    fontSize: '10px',
    marginTop: '6px',
    letterSpacing: '0.02em',
  } as CSSProperties,
  inputWrapper: {
    position: 'relative',
    width: '100%',
    display: 'flex',
    alignItems: 'flex-start',
    overflow: 'hidden', // Prevent button/spinner from escaping
    borderRadius: '28px',
  } as CSSProperties,
  toggleContainer: {
    position: 'absolute',
    left: '10px',
    top: '50%',
    transform: 'translateY(-50%)',
    display: 'flex',
    alignItems: 'center',
    gap: '4px',
    zIndex: 2,
    borderRadius: '20px',
    padding: '3px',
    fontSize: '11px',
    fontWeight: 600,
    boxShadow: '0 1px 3px rgba(0,0,0,0.1)',
  } as CSSProperties,
  toggleOption: {
    padding: '6px 12px',
    borderRadius: '16px',
    cursor: 'pointer',
    transition: 'all 0.25s ease',
    display: 'flex',
    alignItems: 'center',
    gap: '5px',
    border: 'none',
    background: 'transparent',
    whiteSpace: 'nowrap',
    fontWeight: 500,
  } as CSSProperties,
  betaTag: {
    fontSize: '8px',
    fontWeight: 600,
    padding: '2px 5px',
    borderRadius: '4px',
    backgroundColor: 'rgba(239, 68, 68, 0.15)',
    color: '#ef4444',
    textTransform: 'uppercase',
    letterSpacing: '0.3px',
    border: '1px solid rgba(239, 68, 68, 0.25)',
  } as CSSProperties,
  textarea: {
    flex: 1,
    width: '100%',
    minWidth: 0,
    borderRadius: '28px',
    padding: '16px 56px 16px 52px',
    fontSize: '15px',
    fontFamily: 'inherit',
    resize: 'none',
    minHeight: '56px',
    maxHeight: '200px',
    outline: 'none',
    boxSizing: 'border-box',
    overflow: 'hidden',
    lineHeight: '1.5',
    wordWrap: 'break-word',
    overflowWrap: 'break-word',
    whiteSpace: 'pre-wrap',
    letterSpacing: '0.01em',
  } as CSSProperties,
  newButton: {
    position: 'absolute',
    left: '10px',
    top: '50%',
    transform: 'translateY(-50%)',
    border: 'none',
    borderRadius: '50%',
    width: '34px',
    height: '34px',
    minWidth: '34px',
    minHeight: '34px',
    maxWidth: '34px',
    ...FLEX.center,
    flexShrink: 0,
    zIndex: 2,
    boxSizing: 'border-box',
    cursor: 'pointer',
    padding: 0,
  } as CSSProperties,
  submitButton: {
    position: 'absolute',
    right: '10px',
    top: '50%',
    transform: 'translateY(-50%)',
    color: '#ffffff',
    border: 'none',
    borderRadius: '50%',
    width: '38px',
    height: '38px',
    minWidth: '38px',
    minHeight: '38px',
    maxWidth: '38px',
    ...FLEX.center,
    flexShrink: 0,
    zIndex: 2,
    boxSizing: 'border-box',
  } as CSSProperties,
} as const;

// =============================================================================
// COMPONENT
// =============================================================================

/**
 * Search input area with submit button
 * Performance optimized with memoized handlers and styles
 */
export function InputArea() {
  const { themeColors } = useTheme();
  const { performSearch, isSearching, searchMode, setSearchMode } = useStreamingSearch();
  const { state, dispatch, resetChat } = useChatContext();

  const [query, setQuery] = useState('');
  const [isRefreshing, setIsRefreshing] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  // Tool availability state from context (with safety checks)
  const availableTools = state.availableTools || [];
  const toolsLoading = state.toolsLoading ?? true;
  const toolsError = state.toolsError ?? false;
  const toolsAvailable = !toolsLoading && availableTools.length > 0;

  // Derived state
  const completedTurns = useMemo(
    () => state.messages.filter(m => m.type === 'assistant' && !m.isStreaming).length,
    [state.messages]
  );
  const canAskFollowUp = completedTurns >= 1;
  const followUpLimitReached = false;
  const isResearchMode = searchMode === 'research';
  const isDark = themeColors.mode === 'dark';
  const isLanding = state.messages.length === 0;
  // Disable submit if tools not available
  const canSubmit = query.trim() && !isSearching && !followUpLimitReached && toolsAvailable;

  // Handle refresh tools
  const handleRefreshTools = useCallback(async () => {
    setIsRefreshing(true);
    dispatch({ type: 'SET_TOOLS_LOADING', payload: true });
    dispatch({ type: 'SET_TOOLS_ERROR', payload: false });
    try {
      await apiClient.refreshTools();
      const tools = await apiClient.getTools();
      const toolsList = Array.isArray(tools) ? tools : [];
      dispatch({ type: 'SET_AVAILABLE_TOOLS', payload: toolsList });
    } catch (error) {
      dispatch({ type: 'SET_TOOLS_ERROR', payload: true });
    } finally {
      dispatch({ type: 'SET_TOOLS_LOADING', payload: false });
      setIsRefreshing(false);
    }
  }, [dispatch]);

  // Handle re-login
  const handleReLogin = useCallback(() => {
    window.location.href = getBackendUrl('/auth/login');
  }, []);

  // Memoized toggle handler
  const toggleSearchMode = useCallback(() => {
    const newMode: SearchMode = searchMode === 'quick' ? 'research' : 'quick';
    setSearchMode(newMode);
  }, [searchMode, setSearchMode]);

  // Auto-resize textarea
  const adjustTextareaHeight = useCallback(() => {
    const textarea = inputRef.current;
    if (textarea) {
      textarea.style.height = 'auto';
      const scrollHeight = textarea.scrollHeight;
      const maxHeight = 200;
      const newHeight = Math.min(scrollHeight, maxHeight);
      textarea.style.height = `${newHeight}px`;
      textarea.style.overflowY = scrollHeight > maxHeight ? 'auto' : 'hidden';
    }
  }, []);

  useEffect(() => {
    adjustTextareaHeight();
  }, [query, adjustTextareaHeight]);

  // Memoized submit handler
  const handleSubmit = useCallback(() => {
    if (!query.trim() || isSearching) return;
    performSearch(query);
    setQuery('');
    setTimeout(() => inputRef.current?.focus(), 100);
  }, [query, isSearching, performSearch]);

  // Memoized keydown handler
  const handleKeyDown = useCallback((e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  }, [handleSubmit]);

  // Memoized onChange handler
  const handleQueryChange = useCallback((e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setQuery(e.target.value);
  }, []);

  // Pre-computed background colors for mode button
  const modeButtonDefaultBg = useMemo(() =>
    isResearchMode ? `${themeColors.accent}15` : (isDark ? 'rgba(255, 255, 255, 0.06)' : 'rgba(0, 0, 0, 0.04)'),
    [isResearchMode, themeColors.accent, isDark]
  );
  const modeButtonHoverBg = useMemo(() =>
    isResearchMode ? `${themeColors.accent}25` : (isDark ? 'rgba(255, 255, 255, 0.1)' : 'rgba(0, 0, 0, 0.08)'),
    [isResearchMode, themeColors.accent, isDark]
  );

  // Pre-computed border color for textarea
  const textareaBorderColor = useMemo(() =>
    isDark ? 'rgba(255, 255, 255, 0.1)' : 'rgba(0, 0, 0, 0.06)',
    [isDark]
  );

  // Memoized mode button hover handlers
  const handleModeButtonEnter = useCallback((e: React.MouseEvent<HTMLButtonElement>) => {
    if (!isSearching) {
      e.currentTarget.style.backgroundColor = modeButtonHoverBg;
    }
  }, [isSearching, modeButtonHoverBg]);

  const handleModeButtonLeave = useCallback((e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = modeButtonDefaultBg;
  }, [modeButtonDefaultBg]);

  // Memoized textarea focus/blur handlers
  const handleTextareaFocus = useCallback((e: React.FocusEvent<HTMLTextAreaElement>) => {
    e.currentTarget.style.borderColor = themeColors.accent;
    e.currentTarget.style.boxShadow = `0 0 0 2px ${themeColors.accent}30, 0 4px 24px rgba(0,0,0,0.08)`;
  }, [themeColors.accent]);

  const handleTextareaBlur = useCallback((e: React.FocusEvent<HTMLTextAreaElement>) => {
    e.currentTarget.style.borderColor = themeColors.border;
    e.currentTarget.style.boxShadow = isDark
      ? '0 4px 24px rgba(0,0,0,0.4), 0 1px 4px rgba(0,0,0,0.2)'
      : '0 4px 24px rgba(0,0,0,0.06), 0 1px 4px rgba(0,0,0,0.04)';
  }, [themeColors.border, isDark]);

  // Memoized submit button hover handlers
  const handleSubmitEnter = useCallback((e: React.MouseEvent<HTMLButtonElement>) => {
    if (canSubmit) {
      e.currentTarget.style.transform = 'translateY(-50%) scale(1.05)';
    }
  }, [canSubmit]);

  const handleSubmitLeave = useCallback((e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.transform = 'translateY(-50%)';
  }, []);

  // Memoized dynamic styles for toggle
  const toggleContainerStyle = useMemo(() => ({
    ...STYLES.toggleContainer,
    backgroundColor: isDark ? 'rgba(40, 40, 45, 0.9)' : 'rgba(255, 255, 255, 0.95)',
    border: `1.5px solid ${isDark ? 'rgba(255,255,255,0.12)' : 'rgba(0,0,0,0.1)'}`,
    boxShadow: isDark
      ? '0 2px 8px rgba(0,0,0,0.3)'
      : '0 2px 8px rgba(0,0,0,0.08)',
    opacity: isSearching ? 0.6 : 1,
  }), [isDark, isSearching]);

  const quickOptionStyle = useMemo(() => ({
    ...STYLES.toggleOption,
    background: !isResearchMode
      ? (isDark ? 'rgba(52, 211, 153, 0.2)' : 'rgba(16, 185, 129, 0.12)')
      : 'transparent',
    color: !isResearchMode
      ? (isDark ? '#6ee7b7' : '#059669')
      : themeColors.textSecondary,
    cursor: isSearching ? 'not-allowed' : 'pointer',
    border: !isResearchMode
      ? `1px solid ${isDark ? 'rgba(52, 211, 153, 0.3)' : 'rgba(16, 185, 129, 0.25)'}`
      : '1px solid transparent',
  }), [isResearchMode, isDark, themeColors.textSecondary, isSearching]);

  const deepOptionStyle = useMemo(() => ({
    ...STYLES.toggleOption,
    background: isResearchMode
      ? (isDark ? 'rgba(129, 140, 248, 0.2)' : 'rgba(99, 102, 241, 0.12)')
      : 'transparent',
    color: isResearchMode
      ? (isDark ? '#a5b4fc' : '#4f46e5')
      : themeColors.textSecondary,
    cursor: isSearching ? 'not-allowed' : 'pointer',
    border: isResearchMode
      ? `1px solid ${isDark ? 'rgba(129, 140, 248, 0.3)' : 'rgba(99, 102, 241, 0.25)'}`
      : '1px solid transparent',
  }), [isResearchMode, isDark, themeColors.textSecondary, isSearching]);

  const textareaStyle = useMemo(() => ({
    ...STYLES.textarea,
    backgroundColor: isDark ? themeColors.surface : '#ffffff',
    color: themeColors.text,
    border: `1.5px solid ${themeColors.border}`,
    boxShadow: isDark
      ? '0 4px 24px rgba(0,0,0,0.4), 0 1px 4px rgba(0,0,0,0.2)'
      : '0 4px 24px rgba(0,0,0,0.06), 0 1px 4px rgba(0,0,0,0.04)',
    transition: TRANSITION.colors,
  }), [isDark, themeColors]);

  const submitButtonStyle = useMemo(() => ({
    ...STYLES.submitButton,
    background: canSubmit
      ? `linear-gradient(135deg, ${themeColors.accent} 0%, ${themeColors.primary} 100%)`
      : (isDark ? 'rgba(255,255,255,0.08)' : 'rgba(0,0,0,0.06)'),
    color: canSubmit ? '#ffffff' : (isDark ? 'rgba(255,255,255,0.3)' : 'rgba(0,0,0,0.25)'),
    cursor: canSubmit ? 'pointer' : 'not-allowed',
    boxShadow: canSubmit
      ? `0 4px 14px ${themeColors.accent}50`
      : 'none',
    transition: TRANSITION.default,
  }), [canSubmit, isDark, themeColors.accent, themeColors.primary]);

  const newButtonStyle = useMemo(() => ({
    ...STYLES.newButton,
    backgroundColor: isDark ? 'rgba(255,255,255,0.08)' : 'rgba(0,0,0,0.05)',
    color: isDark ? 'rgba(255,255,255,0.5)' : 'rgba(0,0,0,0.4)',
    transition: TRANSITION.default,
  }), [isDark]);

  // Memoized placeholder
  const placeholder = useMemo(() => {
    if (toolsLoading) {
      return "Loading tools...";
    }
    if (!toolsAvailable) {
      return "Tools unavailable - cannot send queries";
    }
    // Deep mode placeholder removed — always quick mode
    return canAskFollowUp
      ? "Ask a follow-up question..."
      : "Ask anything...";
  }, [canAskFollowUp, toolsLoading, toolsAvailable]);

  // Research mode warning styles - positioned below input (compact)
  const researchWarningStyle = useMemo(() => ({
    fontSize: '11px',
    fontWeight: 500,
    color: isDark ? '#fbbf24' : '#d97706',
    backgroundColor: isDark ? 'rgba(245, 158, 11, 0.12)' : 'rgba(245, 158, 11, 0.08)',
    padding: '5px 10px',
    borderRadius: '6px',
    marginTop: '6px',
    lineHeight: '1.4',
    border: `1px solid ${isDark ? 'rgba(245, 158, 11, 0.25)' : 'rgba(245, 158, 11, 0.2)'}`,
    display: 'flex',
    alignItems: 'center',
    gap: '6px',
  }), [isDark]);

  // Warning banner styles
  const toolsWarningBannerStyle = useMemo(() => ({
    backgroundColor: isDark ? 'rgba(239, 68, 68, 0.15)' : 'rgba(239, 68, 68, 0.1)',
    border: `1.5px solid ${isDark ? 'rgba(239, 68, 68, 0.4)' : 'rgba(239, 68, 68, 0.3)'}`,
    borderRadius: '12px',
    padding: '16px 20px',
    marginBottom: '16px',
    display: 'flex',
    flexDirection: 'column' as const,
    gap: '12px',
  }), [isDark]);

  const warningHeaderStyle = useMemo(() => ({
    display: 'flex',
    alignItems: 'center',
    gap: '8px',
    color: isDark ? '#fca5a5' : '#dc2626',
    fontSize: '14px',
    fontWeight: 600,
  }), [isDark]);

  const warningTextStyle = useMemo(() => ({
    color: isDark ? '#e5e7eb' : '#374151',
    fontSize: '13px',
    lineHeight: '1.5',
  }), [isDark]);

  const warningActionsStyle = useMemo(() => ({
    display: 'flex',
    gap: '12px',
    flexWrap: 'wrap' as const,
    alignItems: 'center',
  }), []);

  const actionButtonStyle = useMemo(() => ({
    padding: '8px 16px',
    borderRadius: '8px',
    border: 'none',
    fontSize: '13px',
    fontWeight: 500,
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
    gap: '6px',
    transition: 'all 0.2s ease',
  }), []);

  const refreshButtonStyle = useMemo(() => ({
    ...actionButtonStyle,
    backgroundColor: isDark ? 'rgba(59, 130, 246, 0.2)' : 'rgba(59, 130, 246, 0.15)',
    color: isDark ? '#93c5fd' : '#2563eb',
    border: `1px solid ${isDark ? 'rgba(59, 130, 246, 0.4)' : 'rgba(59, 130, 246, 0.3)'}`,
  }), [actionButtonStyle, isDark]);

  const reloginButtonStyle = useMemo(() => ({
    ...actionButtonStyle,
    backgroundColor: isDark ? 'rgba(156, 163, 175, 0.2)' : 'rgba(107, 114, 128, 0.15)',
    color: isDark ? '#d1d5db' : '#4b5563',
    border: `1px solid ${isDark ? 'rgba(156, 163, 175, 0.4)' : 'rgba(107, 114, 128, 0.3)'}`,
  }), [actionButtonStyle, isDark]);

  const supportTextStyle = useMemo(() => ({
    color: themeColors.textSecondary,
    fontSize: '12px',
  }), [themeColors.textSecondary]);

  // Determine warning message based on state
  const getWarningMessage = () => {
    if (toolsError) {
      return "Unable to connect to tools. This may be due to a network issue or the service being temporarily unavailable.";
    }
    return "No tools are available for your account. Please contact your administrator to request access.";
  };

  return (
    <div className="input-area" style={STYLES.container}>
      {/* Tools Unavailable Warning Banner */}
      {!toolsLoading && !toolsAvailable && (
        <div style={toolsWarningBannerStyle}>
          <div style={warningHeaderStyle}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
              <line x1="12" y1="9" x2="12" y2="13" />
              <line x1="12" y1="17" x2="12.01" y2="17" />
            </svg>
            <span>Tools Unavailable - Queries Disabled</span>
          </div>
          <div style={warningTextStyle}>
            {getWarningMessage()}
          </div>
          <div style={warningActionsStyle}>
            <button
              onClick={handleRefreshTools}
              disabled={isRefreshing}
              style={{
                ...refreshButtonStyle,
                opacity: isRefreshing ? 0.7 : 1,
                cursor: isRefreshing ? 'not-allowed' : 'pointer',
              }}
              onMouseEnter={(e) => {
                if (!isRefreshing) {
                  e.currentTarget.style.backgroundColor = isDark ? 'rgba(59, 130, 246, 0.3)' : 'rgba(59, 130, 246, 0.25)';
                }
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.backgroundColor = isDark ? 'rgba(59, 130, 246, 0.2)' : 'rgba(59, 130, 246, 0.15)';
              }}
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                style={{
                  animation: isRefreshing ? 'spin 1s linear infinite' : 'none',
                }}
              >
                <polyline points="23 4 23 10 17 10" />
                <polyline points="1 20 1 14 7 14" />
                <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
              </svg>
              {isRefreshing ? 'Refreshing...' : 'Refresh Tools'}
            </button>
            <button
              onClick={handleReLogin}
              style={reloginButtonStyle}
              onMouseEnter={(e) => {
                e.currentTarget.style.backgroundColor = isDark ? 'rgba(156, 163, 175, 0.3)' : 'rgba(107, 114, 128, 0.25)';
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.backgroundColor = isDark ? 'rgba(156, 163, 175, 0.2)' : 'rgba(107, 114, 128, 0.15)';
              }}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
                <polyline points="16 17 21 12 16 7" />
                <line x1="21" y1="12" x2="9" y2="12" />
              </svg>
              Re-login
            </button>
            <span style={supportTextStyle}>
              If issue persists, please raise a support ticket.
            </span>
          </div>
        </div>
      )}

      <div style={STYLES.inputWrapper}>
        {/* DEEP MODE TOGGLE - disabled, see ENABLE_DEEP_MODE.md to re-enable */}
        {false && (
        <div style={{
          ...toggleContainerStyle,
          opacity: toolsAvailable ? (isSearching ? 0.6 : 1) : 0.5,
        }}>
          <button
            onClick={() => !isSearching && toolsAvailable && isResearchMode && toggleSearchMode()}
            disabled={isSearching || !toolsAvailable}
            style={quickOptionStyle}
            title="Quick Search mode"
          >
            <Icon name="lightning" size={12} color="currentColor" strokeWidth={2.5} />
            <span>Quick</span>
          </button>
          <button
            onClick={() => !isSearching && toolsAvailable && !isResearchMode && toggleSearchMode()}
            disabled={isSearching || !toolsAvailable}
            style={deepOptionStyle}
            title="Deep Research mode (uses 4-5x more resources)"
          >
            <Icon name="search-deep" size={12} color="currentColor" strokeWidth={2.5} />
            <span>Deep</span>
            <span style={STYLES.betaTag}>Beta</span>
          </button>
        </div>
        )}

        {/* Left action button: hamburger on landing, + on conversation */}
        <button
          onClick={isLanding
            ? () => window.dispatchEvent(new CustomEvent('toggle-sidebar'))
            : resetChat
          }
          title={isLanding ? 'Menu' : 'New chat'}
          style={newButtonStyle}
          onMouseEnter={(e) => {
            e.currentTarget.style.backgroundColor = isDark ? 'rgba(255,255,255,0.14)' : 'rgba(0,0,0,0.09)';
            e.currentTarget.style.color = isDark ? 'rgba(255,255,255,0.7)' : 'rgba(0,0,0,0.6)';
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.backgroundColor = isDark ? 'rgba(255,255,255,0.08)' : 'rgba(0,0,0,0.05)';
            e.currentTarget.style.color = isDark ? 'rgba(255,255,255,0.5)' : 'rgba(0,0,0,0.4)';
          }}
        >
          {isLanding ? (
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="4" y1="7" x2="20" y2="7" />
              <line x1="4" y1="12" x2="20" y2="12" />
              <line x1="4" y1="17" x2="20" y2="17" />
            </svg>
          ) : (
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
          )}
        </button>

        {/* Textarea */}
        <textarea
          ref={inputRef}
          value={query}
          onChange={handleQueryChange}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          disabled={isSearching || followUpLimitReached || !toolsAvailable}
          style={{
            ...textareaStyle,
            opacity: toolsAvailable ? 1 : 0.6,
            cursor: toolsAvailable ? 'text' : 'not-allowed',
          }}
          onFocus={handleTextareaFocus}
          onBlur={handleTextareaBlur}
          rows={1}
        />

        {/* Submit Button */}
        <button
          onClick={handleSubmit}
          disabled={!canSubmit}
          style={submitButtonStyle}
          onMouseEnter={handleSubmitEnter}
          onMouseLeave={handleSubmitLeave}
        >
          <Icon
            name={isSearching ? 'spinner' : 'send'}
            size={18}
            color="currentColor"
            strokeWidth={2.5}
          />
        </button>
      </div>

      {/* DEEP MODE WARNING - disabled, see ENABLE_DEEP_MODE.md to re-enable */}
      {false && (
        <div style={researchWarningStyle}>
          <span style={{ fontSize: '10px' }}>⚡</span>
          <span>Deep mode uses 4-5x more resources. Use for complex research only.</span>
        </div>
      )}



      <style>{`
        @keyframes spin {
          from { transform: rotate(0deg); }
          to { transform: rotate(360deg); }
        }
      `}</style>
    </div>
  );
}
