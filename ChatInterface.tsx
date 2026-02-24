import { useState, useEffect, useRef } from 'react';
import { MessageList } from './MessageList';
import { InputArea } from './InputArea';
import { HistorySidebar } from './HistorySidebar';
import { ExportPdfButton } from './ExportPdfButton';
import { useTheme } from '../contexts/ThemeContext';
import { useChatContext } from '../contexts/ChatContext';
import { apiClient } from '../services/api';
import { historyService } from '../services/historyService';
import { getBackendUrl, UI_CONFIG } from '../config';
import { TRANSITION } from '../styles/animations';
import { IconButton } from './Button';

/**
 * Main chat interface component
 */
interface ModelInfo {
  id: string;
  name: string;
  description?: string;
  provider?: string;
}

export function ChatInterface() {
  const { themeColors } = useTheme();
  const { state, dispatch } = useChatContext();
  const [availableModels, setAvailableModels] = useState<ModelInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModelDropdown, setShowModelDropdown] = useState(false);
  const [showToolsDropdown, setShowToolsDropdown] = useState(false);
  const [toolsNotification, setToolsNotification] = useState<string | null>(null);
  const [showHistorySidebar, setShowHistorySidebar] = useState(false);
  const [showPreferencesPanel, setShowPreferencesPanel] = useState(false);
  const [userInstructions, setUserInstructions] = useState('');
  const [instructionsSaving, setInstructionsSaving] = useState(false);
  const [unviewedShareCount, setUnviewedShareCount] = useState(0);
  const dropdownRef = useRef<HTMLDivElement>(null);
  const toolsDropdownRef = useRef<HTMLDivElement>(null);

  // Derive tools state from context (with safety checks)
  const availableTools = state.availableTools || [];
  const toolsLoading = state.toolsLoading ?? true;

  // Close model dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setShowModelDropdown(false);
      }
    };

    if (showModelDropdown) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => {
        document.removeEventListener('mousedown', handleClickOutside);
      };
    }
  }, [showModelDropdown]);

  // Close tools dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (toolsDropdownRef.current && !toolsDropdownRef.current.contains(event.target as Node)) {
        setShowToolsDropdown(false);
      }
    };

    if (showToolsDropdown) {
      document.addEventListener('mousedown', handleClickOutside);
      return () => {
        document.removeEventListener('mousedown', handleClickOutside);
      };
    }
  }, [showToolsDropdown]);

  // Load user profile on mount
  useEffect(() => {
    async function loadUserProfile() {
      try {
        const userData = await apiClient.getUser();
        dispatch({ type: 'SET_USER', payload: userData });
      } catch (error) {
        console.error('Failed to load user info:', error);
        // Set fallback user if fetch fails
        dispatch({ type: 'SET_USER', payload: { email: 'Guest', name: 'Guest' } });
      }
    }

    loadUserProfile();
  }, [dispatch]);

  // Load user preferences on mount
  useEffect(() => {
    async function loadPreferences() {
      try {
        const instructions = await historyService.getPreferences();
        setUserInstructions(instructions);
      } catch (error) {
        console.error('Failed to load preferences:', error);
      }
    }
    loadPreferences();
  }, []);

  // Load unviewed share count on mount and refresh periodically
  useEffect(() => {
    async function loadUnviewedCount() {
      try {
        const count = await historyService.getUnviewedShareCount();
        setUnviewedShareCount(count);
      } catch (error) {
        console.error('Failed to load unviewed share count:', error);
      }
    }
    loadUnviewedCount();

    // Refresh every 60 seconds
    const interval = setInterval(loadUnviewedCount, 60000);
    return () => clearInterval(interval);
  }, []);

  // Load available models on mount
  useEffect(() => {
    async function loadModels() {
      try {
        const response = await apiClient.getModels();

        // Flatten models from all providers, keeping track of provider
        const allModels: ModelInfo[] = [];
        Object.entries(response.models || {}).forEach(([provider, models]) => {

          (models as ModelInfo[]).forEach((model) => {
            allModels.push({
              ...model,
              provider: provider, // Track which provider this model belongs to
            });
          });
        });

        setAvailableModels(allModels);

        // Set default model and provider if not already set
        if (response.defaults && allModels.length > 0) {
          const defaultProvider = response.defaults.provider;
          const defaultModel = response.defaults.models?.[defaultProvider];


          if (defaultModel && !state.selectedModel) {

            dispatch({ type: 'SET_LLM_PROVIDER', payload: defaultProvider });
            dispatch({ type: 'SET_LLM_MODEL', payload: defaultModel });
          }
        }
      } catch (error) {

      } finally {
        setLoading(false);
      }
    }

    loadModels();
  }, [dispatch, state.selectedModel]);

  // Load available tools on mount (runs once)
  useEffect(() => {
    let isMounted = true;
    async function loadTools() {
      dispatch({ type: 'SET_TOOLS_LOADING', payload: true });
      dispatch({ type: 'SET_TOOLS_ERROR', payload: false });
      try {
        const tools = await apiClient.getTools();
        if (!isMounted) return;
        const toolsList = Array.isArray(tools) ? tools : [];
        dispatch({ type: 'SET_AVAILABLE_TOOLS', payload: toolsList });
        // Set enabled tools to those marked as enabled by default
        const enabledToolNames = toolsList
          .filter((t: any) => t.enabled)
          .map((t: any) => t.name);
        dispatch({ type: 'SET_ENABLED_TOOLS', payload: enabledToolNames });
      } catch (error) {
        if (!isMounted) return;
        dispatch({ type: 'SET_AVAILABLE_TOOLS', payload: [] });
        dispatch({ type: 'SET_TOOLS_ERROR', payload: true });
      } finally {
        if (isMounted) {
          dispatch({ type: 'SET_TOOLS_LOADING', payload: false });
        }
      }
    }
    loadTools();
    return () => { isMounted = false; };
  }, [dispatch]);

  // Polling every 30 seconds + tab visibility refresh (silently update state)
  useEffect(() => {
    const checkTools = async () => {
      try {
        await apiClient.refreshTools();
        const tools = await apiClient.getTools();
        const toolsList = Array.isArray(tools) ? tools : [];
        dispatch({ type: 'SET_AVAILABLE_TOOLS', payload: toolsList });
        dispatch({ type: 'SET_TOOLS_ERROR', payload: false });
      } catch {
        dispatch({ type: 'SET_TOOLS_ERROR', payload: true });
      }
    };

    const interval = setInterval(checkTools, 30000);
    const handleVisibility = () => {
      if (document.visibilityState === 'visible') checkTools();
    };
    document.addEventListener('visibilitychange', handleVisibility);

    return () => {
      clearInterval(interval);
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [dispatch]);

  // Listen for tools-unavailable event (fired after conversation turn in useStreamingSearch)
  // This handles mid-session tool loss - shows a brief notification
  useEffect(() => {
    const handleToolsUnavailable = async () => {
      // Only show notification for mid-session loss (when user was previously working)
      // The banner in InputArea handles the persistent warning
      if (state.messages.length > 0) {
        setToolsNotification('Connection to tools lost. Check the input area for options.');
        setTimeout(() => setToolsNotification(null), 4000);
      }
    };

    window.addEventListener('tools-unavailable', handleToolsUnavailable);
    return () => window.removeEventListener('tools-unavailable', handleToolsUnavailable);
  }, [state.messages.length]);

  // Save conversation when response completes (so feedback can be submitted)
  useEffect(() => {
    const handleSaveConversation = async () => {
      if (state.messages.length > 0) {
        await historyService.saveConversation(state.sessionId, state.messages);
      }
    };

    window.addEventListener('save-conversation', handleSaveConversation);
    return () => window.removeEventListener('save-conversation', handleSaveConversation);
  }, [state.sessionId, state.messages]);


  const handleToolToggle = (toolName: string) => {
    const currentTools = state.enabledTools || [];
    const newTools = currentTools.includes(toolName)
      ? currentTools.filter(t => t !== toolName)
      : [...currentTools, toolName];

    dispatch({ type: 'SET_ENABLED_TOOLS', payload: newTools });
  };

  const handleRefreshTools = async () => {
    dispatch({ type: 'SET_TOOLS_LOADING', payload: true });
    dispatch({ type: 'SET_TOOLS_ERROR', payload: false });
    try {
      await apiClient.refreshTools();
      const tools = await apiClient.getTools();
      const toolsList = Array.isArray(tools) ? tools : [];
      dispatch({ type: 'SET_AVAILABLE_TOOLS', payload: toolsList });
      if (toolsList.length > 0) {
        setToolsNotification('Tools connected!');
        setTimeout(() => setToolsNotification(null), 3000);
      }
    } catch (error) {
      dispatch({ type: 'SET_TOOLS_ERROR', payload: true });
      setToolsNotification('Failed to refresh tools. Please try again.');
      setTimeout(() => setToolsNotification(null), 5000);
    } finally {
      dispatch({ type: 'SET_TOOLS_LOADING', payload: false });
    }
  };

  const handleLogout = async () => {
    // Save conversation before logout
    if (state.messages.length > 0) {
      await historyService.saveConversation(state.sessionId, state.messages);
    }
    try {
      await apiClient.logout();
      // Redirect to backend login page after successful logout
      window.location.href = getBackendUrl('/auth/login');
    } catch (error) {

      // Even if logout API fails, redirect to login
      window.location.href = getBackendUrl('/auth/login');
    }
  };

  const handleSavePreferences = async () => {
    setInstructionsSaving(true);
    try {
      await historyService.savePreferences(userInstructions);
      setShowPreferencesPanel(false);
    } catch (error) {
      console.error('Failed to save preferences:', error);
    } finally {
      setInstructionsSaving(false);
    }
  };

  // Load conversation from history (own or shared)
  const handleLoadConversation = async (conversationId: string, isShared?: boolean) => {
    try {
      const conversation = isShared
        ? await historyService.getSharedConversation(conversationId)
        : await historyService.getConversation(conversationId);

      if (conversation && conversation.messages) {
        dispatch({
          type: 'LOAD_CONVERSATION',
          payload: {
            sessionId: conversation.id,
            messages: conversation.messages,
            isShared: isShared || false,
          },
        });
      }
    } catch (error) {
      console.error('Error loading conversation:', error);
    }
  };

  const handleModelChange = (modelId: string) => {
    // Find the model to get its provider
    const selectedModel = availableModels.find(m => m.id === modelId);
    if (selectedModel && selectedModel.provider) {
      dispatch({ type: 'SET_LLM_PROVIDER', payload: selectedModel.provider });
      dispatch({ type: 'SET_LLM_MODEL', payload: modelId });
    }
  };

  // Model icon mapping - returns SVG for cross-platform compatibility
  const getModelIcon = (modelId: string) => {
    // Brain icon for Claude - subtle purple
    if (modelId.includes('claude')) {
      return (
        <div style={{ padding: '4px', border: '1px solid rgba(156, 39, 176, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(156, 39, 176, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M9.5 2A2.5 2.5 0 0 1 12 4.5v15a2.5 2.5 0 0 1-4.96.44 2.5 2.5 0 0 1-2.96-3.08 3 3 0 0 1-.34-5.58 2.5 2.5 0 0 1 1.32-4.24 2.5 2.5 0 0 1 1.98-3A2.5 2.5 0 0 1 9.5 2Z" />
            <path d="M14.5 2A2.5 2.5 0 0 0 12 4.5v15a2.5 2.5 0 0 0 4.96.44 2.5 2.5 0 0 0 2.96-3.08 3 3 0 0 0 .34-5.58 2.5 2.5 0 0 0-1.32-4.24 2.5 2.5 0 0 0-1.98-3A2.5 2.5 0 0 0 14.5 2Z" />
          </svg>
        </div>
      );
    }
    // Llama - use a CPU/processor icon - subtle orange
    if (modelId.includes('llama')) {
      return (
        <div style={{ padding: '4px', border: '1px solid rgba(255, 152, 0, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(255, 152, 0, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="4" y="4" width="16" height="16" rx="2" />
            <rect x="9" y="9" width="6" height="6" />
            <line x1="9" y1="1" x2="9" y2="4" />
            <line x1="15" y1="1" x2="15" y2="4" />
            <line x1="9" y1="20" x2="9" y2="23" />
            <line x1="15" y1="20" x2="15" y2="23" />
            <line x1="20" y1="9" x2="23" y2="9" />
            <line x1="20" y1="15" x2="23" y2="15" />
            <line x1="1" y1="9" x2="4" y2="9" />
            <line x1="1" y1="15" x2="4" y2="15" />
          </svg>
        </div>
      );
    }
    // Qwen - use a zap/lightning icon - subtle pink
    if (modelId.includes('qwen')) {
      return (
        <div style={{ padding: '4px', border: '1px solid rgba(233, 30, 99, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(233, 30, 99, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />
          </svg>
        </div>
      );
    }
    // Mistral - use a wind/waves icon - subtle cyan
    if (modelId.includes('mistral')) {
      return (
        <div style={{ padding: '4px', border: '1px solid rgba(0, 188, 212, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(0, 188, 212, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M9.59 4.59A2 2 0 1 1 11 8H2m10.59 11.41A2 2 0 1 0 14 16H2m15.73-8.27A2.5 2.5 0 1 1 19.5 12H2" />
          </svg>
        </div>
      );
    }
    // Gemma - use a gem/diamond icon - subtle teal
    if (modelId.includes('gemma')) {
      return (
        <div style={{ padding: '4px', border: '1px solid rgba(0, 150, 136, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(0, 150, 136, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polygon points="12 2 2 7 12 12 22 7 12 2" />
            <polyline points="2 17 12 22 22 17" />
            <polyline points="2 12 12 17 22 12" />
          </svg>
        </div>
      );
    }
    // Default robot icon - subtle blue-gray
    return (
      <div style={{ padding: '4px', border: '1px solid rgba(96, 125, 139, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(96, 125, 139, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="11" width="18" height="10" rx="2" />
          <circle cx="12" cy="5" r="2" />
          <path d="M12 7v4" />
          <line x1="8" y1="16" x2="8" y2="16" />
          <line x1="16" y1="16" x2="16" y2="16" />
        </svg>
      </div>
    );
  };

  return (
    <div
      className="chat-interface"
      style={{
        display: 'flex',
        height: '100vh',
        width: '100%',
        maxWidth: '100vw',
        overflow: 'hidden',
        backgroundColor: themeColors.background,
        color: themeColors.text,
        fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
      }}
    >
      {/* History Sidebar */}
      <HistorySidebar
        isOpen={showHistorySidebar}
        onClose={() => setShowHistorySidebar(false)}
        onLoadConversation={handleLoadConversation}
      />

      {/* Preferences Panel */}
      {showPreferencesPanel && (
        <>
          {/* Backdrop */}
          <div
            onClick={() => setShowPreferencesPanel(false)}
            style={{
              position: 'fixed',
              inset: 0,
              backgroundColor: 'rgba(0, 0, 0, 0.3)',
              zIndex: 999,
            }}
          />
          {/* Panel */}
          <div
            style={{
              position: 'fixed',
              left: '72px',
              top: '16px',
              bottom: '16px',
              width: '350px',
              backgroundColor: themeColors.surface,
              borderRadius: '16px',
              boxShadow: '0 8px 32px rgba(0, 0, 0, 0.2)',
              zIndex: 1000,
              display: 'flex',
              flexDirection: 'column',
              overflow: 'hidden',
            }}
          >
            {/* Header */}
            <div
              style={{
                padding: '16px 20px',
                borderBottom: `1px solid ${themeColors.border}`,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
              }}
            >
              <h3 style={{ margin: 0, fontSize: '16px', fontWeight: '600', color: themeColors.text }}>
                Agent Instructions
              </h3>
              <button
                onClick={() => setShowPreferencesPanel(false)}
                style={{
                  background: 'none',
                  border: 'none',
                  cursor: 'pointer',
                  padding: '4px',
                  color: themeColors.textSecondary,
                }}
              >
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <line x1="18" y1="6" x2="6" y2="18" />
                  <line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
            </div>

            {/* Content */}
            <div style={{ flex: 1, padding: '20px', display: 'flex', flexDirection: 'column', gap: '16px' }}>
              <p style={{ margin: 0, fontSize: '13px', color: themeColors.textSecondary }}>
                Provide instructions for the agent. These will be applied to all your conversations.
              </p>
              <textarea
                value={userInstructions}
                onChange={(e) => setUserInstructions(e.target.value)}
                placeholder="e.g., Always show results in table format. Focus on India region by default."
                style={{
                  flex: 1,
                  padding: '12px',
                  borderRadius: '8px',
                  border: `1px solid ${themeColors.border}`,
                  backgroundColor: themeColors.background,
                  color: themeColors.text,
                  fontSize: '13px',
                  resize: 'none',
                  outline: 'none',
                  fontFamily: 'inherit',
                }}
              />
              <button
                onClick={handleSavePreferences}
                disabled={instructionsSaving}
                style={{
                  padding: '10px 16px',
                  borderRadius: '8px',
                  border: 'none',
                  backgroundColor: '#9C27B0',
                  color: 'white',
                  fontSize: '13px',
                  fontWeight: '500',
                  cursor: instructionsSaving ? 'not-allowed' : 'pointer',
                  opacity: instructionsSaving ? 0.7 : 1,
                }}
              >
                {instructionsSaving ? 'Saving...' : 'Save Instructions'}
              </button>
            </div>
          </div>
        </>
      )}

      {/* Tools notification toast */}
      {toolsNotification && (
        <div
          style={{
            position: 'fixed',
            top: '16px',
            left: '50%',
            transform: 'translateX(-50%)',
            backgroundColor: toolsNotification.includes('connected!')
              ? themeColors.success
              : toolsNotification.includes('Reconnecting')
                ? themeColors.info
                : themeColors.error,
            color: 'white',
            padding: '10px 20px',
            borderRadius: '8px',
            fontSize: '13px',
            fontWeight: '500',
            zIndex: 9999,
            boxShadow: '0 4px 12px rgba(0,0,0,0.25)',
          }}
        >
          {toolsNotification}
        </div>
      )}

      {/* Left sidebar - New conversation & Logout */}
      <div
        style={{
          position: 'fixed',
          left: 0,
          top: 0,
          bottom: 0,
          width: '64px',
          backgroundColor: 'transparent',
          paddingTop: '16px',
          paddingBottom: '16px',
          paddingLeft: '6px',
          paddingRight: '6px',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'space-between',
          alignItems: 'center',
          zIndex: 50,
        }}
      >
        {/* Title at top - only show when there are messages */}
        <div style={{
          writingMode: 'vertical-rl',
          textOrientation: 'mixed',
          fontSize: '14px',
          fontWeight: '600',
          color: themeColors.text,
          marginTop: '24px',
          letterSpacing: '2px',
          opacity: state.messages.length > 0 ? 1 : 0,
          transition: TRANSITION.opacity,
        }}>
          AGENTIC SEARCH
        </div>

        {/* Bottom Section - All Icons */}
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '12px', marginBottom: '80px' }}>
          {/* Conversations Section */}
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2px', position: 'relative' }}>
            <div
              style={{
                fontSize: '9px',
                color: themeColors.textSecondary,
                fontWeight: '600',
                textTransform: 'uppercase',
                letterSpacing: '0.5px',
              }}
            >
              Convos
            </div>
            <button
              onClick={() => setShowHistorySidebar(true)}
              title="View Conversations"
              style={{
                width: '40px',
                height: '40px',
                borderRadius: '10px',
                border: '1px solid rgba(255, 255, 255, 0.1)',
                backgroundColor: 'transparent',
                cursor: 'pointer',
                fontSize: '20px',
                transition: TRANSITION.slow,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                color: themeColors.text,
                willChange: 'transform, background-color, border-color',
                position: 'relative',
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.backgroundColor = '#FF980015';
                e.currentTarget.style.borderColor = '#FF9800';
                e.currentTarget.style.transform = 'scale(1.05)';
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.backgroundColor = 'transparent';
                e.currentTarget.style.borderColor = 'rgba(255, 255, 255, 0.1)';
                e.currentTarget.style.transform = 'scale(1)';
              }}
            >
              <div style={{ padding: '2.5px', border: '1px solid rgba(255, 152, 0, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(255, 152, 0, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="12" cy="12" r="10" />
                  <polyline points="12 6 12 12 16 14" />
                </svg>
              </div>
              {/* Notification Badge */}
              {unviewedShareCount > 0 && (
                <span
                  style={{
                    position: 'absolute',
                    top: '-4px',
                    right: '-4px',
                    backgroundColor: '#E91E63',
                    color: 'white',
                    fontSize: '9px',
                    fontWeight: '700',
                    minWidth: '16px',
                    height: '16px',
                    borderRadius: '8px',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    padding: '0 4px',
                    boxShadow: '0 2px 4px rgba(0,0,0,0.3)',
                  }}
                >
                  {unviewedShareCount > 9 ? '9+' : unviewedShareCount}
                </span>
              )}
            </button>
          </div>

          {/* Preferences Section */}
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2px' }}>
            <div
              style={{
                fontSize: '9px',
                color: themeColors.textSecondary,
                fontWeight: '600',
                textTransform: 'uppercase',
                letterSpacing: '0.5px',
              }}
            >
              Agent
            </div>
            <button
              onClick={() => setShowPreferencesPanel(true)}
              title="Agent Preferences"
              style={{
                width: '40px',
                height: '40px',
                borderRadius: '10px',
                border: '1px solid rgba(255, 255, 255, 0.1)',
                backgroundColor: 'transparent',
                cursor: 'pointer',
                fontSize: '20px',
                transition: TRANSITION.slow,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                color: themeColors.text,
                willChange: 'transform, background-color, border-color',
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.backgroundColor = '#9C27B015';
                e.currentTarget.style.borderColor = '#9C27B0';
                e.currentTarget.style.transform = 'scale(1.05)';
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.backgroundColor = 'transparent';
                e.currentTarget.style.borderColor = 'rgba(255, 255, 255, 0.1)';
                e.currentTarget.style.transform = 'scale(1)';
              }}
            >
              <div style={{ padding: '2.5px', border: '1px solid rgba(156, 39, 176, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(156, 39, 176, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="12" cy="12" r="3" />
                  <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
                </svg>
              </div>
            </button>
          </div>

          {/* Export PDF Section */}
          {state.messages.length > 0 && (
            <ExportPdfButton
              conversationElementId="main-scroll-container"
              chartElementId="chart-container"
              disabled={state.isLoading}
            />
          )}

          {/* New Conversation Section */}
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2px' }}>
            <div
              style={{
                fontSize: '9px',
                color: themeColors.textSecondary,
                fontWeight: '600',
                textTransform: 'uppercase',
                letterSpacing: '0.5px',
              }}
            >
              New
            </div>
            <button
              onClick={async () => {
                // Save current conversation before starting new one
                if (state.messages.length > 0) {
                  await historyService.saveConversation(state.sessionId, state.messages);
                }
                // Check tools before new conversation - notify if empty
                try {
                  await apiClient.refreshTools();
                  const tools = await apiClient.getTools();
                  if (!tools || tools.length === 0) {
                    window.dispatchEvent(new CustomEvent('tools-unavailable'));
                    return; // Don't reload, let user see notification
                  }
                } catch {
                  window.dispatchEvent(new CustomEvent('tools-unavailable'));
                  return;
                }
                window.location.reload();
              }}
              title="New Conversation"
              style={{
                width: '40px',
                height: '40px',
                borderRadius: '10px',
                border: '1px solid rgba(255, 255, 255, 0.1)',
                backgroundColor: 'transparent',
                cursor: 'pointer',
                fontSize: '20px',
                transition: TRANSITION.slow,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                color: themeColors.text,
                willChange: 'transform, background-color, border-color',
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.backgroundColor = '#2196F315';
                e.currentTarget.style.borderColor = '#2196F3';
                e.currentTarget.style.transform = 'scale(1.05)';
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.backgroundColor = 'transparent';
                e.currentTarget.style.borderColor = 'rgba(255, 255, 255, 0.1)';
                e.currentTarget.style.transform = 'scale(1)';
              }}
            >
              <div style={{ padding: '2.5px', border: '1px solid rgba(33, 150, 243, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(33, 150, 243, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <line x1="12" y1="5" x2="12" y2="19" />
                  <line x1="5" y1="12" x2="19" y2="12" />
                </svg>
              </div>
            </button>
          </div>

          {/* Model Selector */}
          {!UI_CONFIG.hideModelSelector && <div ref={dropdownRef} style={{ position: 'relative' }}>
            {/* Model Selector Label */}
            <div
              style={{
                fontSize: '9px',
                color: themeColors.textSecondary,
                fontWeight: '600',
                textTransform: 'uppercase',
                letterSpacing: '0.5px',
                marginBottom: '2px',
              }}
            >
              Model
            </div>

            {/* Model Selector Button */}
            {loading ? (
              <div style={{
                fontSize: '10px',
                color: themeColors.textSecondary,
                display: 'flex',
                alignItems: 'center',
                gap: '4px',
              }}>
                <style>{`
                  @keyframes spinDot {
                    0%, 100% { transform: scale(0.8); opacity: 0.4; }
                    50% { transform: scale(1.2); opacity: 1; }
                  }
                `}</style>
                <span style={{ animation: 'spinDot 1s ease-in-out infinite 0s' }}>•</span>
                <span style={{ animation: 'spinDot 1s ease-in-out infinite 0.2s' }}>•</span>
                <span style={{ animation: 'spinDot 1s ease-in-out infinite 0.4s' }}>•</span>
              </div>
            ) : (
              <>
                <button
                  onClick={() => setShowModelDropdown(!showModelDropdown)}
                  title={state.selectedModel ? `Current: ${availableModels.find(m => m.id === state.selectedModel)?.name || state.selectedModel}` : 'Select Model'}
                  style={{
                    width: '40px',
                    height: '40px',
                    borderRadius: '10px',
                    border: '1px solid rgba(255, 255, 255, 0.1)',
                    backgroundColor: 'transparent',
                    cursor: 'pointer',
                    fontSize: '18px',
                    transition: TRANSITION.slow,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    willChange: 'transform, background-color',
                  }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.backgroundColor = '#9C27B015';
                    e.currentTarget.style.borderColor = '#9C27B0';
                    e.currentTarget.style.transform = 'scale(1.05)';
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.backgroundColor = 'transparent';
                    e.currentTarget.style.borderColor = 'rgba(255, 255, 255, 0.1)';
                    e.currentTarget.style.transform = 'scale(1)';
                  }}
                >
                  {getModelIcon(state.selectedModel || availableModels[0]?.id || '')}
                </button>

                {/* Dropdown Menu */}
                {showModelDropdown && (
                  <div
                    style={{
                      position: 'absolute',
                      left: '64px',
                      top: '32px',
                      backgroundColor: themeColors.surface,
                      border: `1px solid ${themeColors.border}`,
                      borderRadius: '8px',
                      padding: '4px',
                      minWidth: '200px',
                      maxHeight: '300px',
                      overflowY: 'auto',
                      boxShadow: '0 2px 8px rgba(0, 0, 0, 0.1)',
                      zIndex: 1000,
                    }}
                  >
                    {availableModels.map((model) => {
                      const isSelected = state.selectedModel === model.id;

                      return (
                        <button
                          key={model.id}
                          onClick={() => {
                            handleModelChange(model.id);
                            setShowModelDropdown(false);
                          }}
                          style={{
                            width: '100%',
                            display: 'flex',
                            alignItems: 'center',
                            gap: '8px',
                            padding: '6px 8px',
                            borderRadius: '6px',
                            border: 'none',
                            backgroundColor: isSelected ? `${themeColors.accent}15` : 'transparent',
                            cursor: 'pointer',
                            textAlign: 'left',
                            transition: TRANSITION.default,
                            marginBottom: '2px',
                          }}
                          onMouseEnter={(e) => {
                            if (!isSelected) {
                              e.currentTarget.style.backgroundColor = `${themeColors.accent}08`;
                            }
                          }}
                          onMouseLeave={(e) => {
                            if (!isSelected) {
                              e.currentTarget.style.backgroundColor = 'transparent';
                            }
                          }}
                        >
                          <div style={{ fontSize: '16px', flexShrink: 0 }}>
                            {getModelIcon(model.id)}
                          </div>
                          <div style={{ flex: 1, overflow: 'hidden' }}>
                            <div
                              style={{
                                fontSize: '12px',
                                fontWeight: isSelected ? '600' : '500',
                                color: themeColors.text,
                                whiteSpace: 'nowrap',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                              }}
                            >
                              {model.name}
                            </div>
                            {model.description && (
                              <div
                                style={{
                                  fontSize: '10px',
                                  color: themeColors.textSecondary,
                                  whiteSpace: 'nowrap',
                                  overflow: 'hidden',
                                  textOverflow: 'ellipsis',
                                }}
                              >
                                {model.description}
                              </div>
                            )}
                          </div>
                          {isSelected && (
                            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={themeColors.accent} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                              <polyline points="20 6 9 17 4 12" />
                            </svg>
                          )}
                        </button>
                      );
                    })}
                  </div>
                )}
              </>
            )}
          </div>}

          {/* Tools Section */}
          {!UI_CONFIG.hideToolsSelector && <div ref={toolsDropdownRef} style={{ position: 'relative', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2px' }}>
            <div
              style={{
                fontSize: '9px',
                color: themeColors.textSecondary,
                fontWeight: '600',
                textTransform: 'uppercase',
                letterSpacing: '0.5px',
              }}
            >
              Tools
            </div>

            {toolsLoading ? (
              <button
                disabled
                title="Loading tools..."
                style={{
                  width: '40px',
                  height: '40px',
                  borderRadius: '10px',
                  border: '1px solid rgba(255, 255, 255, 0.1)',
                  backgroundColor: 'transparent',
                  cursor: 'not-allowed',
                  fontSize: '18px',
                  opacity: 0.5,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <div style={{ padding: '4px', border: '1px solid rgba(76, 175, 80, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(76, 175, 80, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
                  </svg>
                </div>
              </button>
            ) : (
              <>
                <button
                  onClick={() => setShowToolsDropdown(!showToolsDropdown)}
                  title={`${state.enabledTools?.length || 0} tool(s) selected`}
                  style={{
                    width: '40px',
                    height: '40px',
                    borderRadius: '10px',
                    border: '1px solid rgba(255, 255, 255, 0.1)',
                    backgroundColor: 'transparent',
                    cursor: 'pointer',
                    fontSize: '18px',
                    transition: TRANSITION.default,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.backgroundColor = `${themeColors.success}15`;
                    e.currentTarget.style.borderColor = themeColors.success;
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.backgroundColor = 'transparent';
                    e.currentTarget.style.borderColor = 'rgba(255, 255, 255, 0.1)';
                  }}
                >
                  <div style={{ padding: '4px', border: '1px solid rgba(76, 175, 80, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(76, 175, 80, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
                    </svg>
                  </div>
                </button>

                {/* Tools Dropdown */}
                {showToolsDropdown && (
                  <div
                    style={{
                      position: 'absolute',
                      left: '64px',
                      bottom: '32px',
                      backgroundColor: themeColors.surface,
                      border: `1px solid ${themeColors.border}`,
                      borderRadius: '8px',
                      padding: '4px',
                      minWidth: '180px',
                      maxHeight: '300px',
                      overflowY: 'auto',
                      boxShadow: '0 2px 8px rgba(0, 0, 0, 0.1)',
                      zIndex: 1000,
                    }}
                  >
                    {availableTools.map((tool) => {
                      const isSelected = state.enabledTools?.includes(tool.name) || false;
                      return (
                        <button
                          key={tool.name}
                          onClick={() => handleToolToggle(tool.name)}
                          title={tool.description || 'No description'}
                          style={{
                            width: '100%',
                            display: 'flex',
                            alignItems: 'center',
                            gap: '8px',
                            padding: '6px 8px',
                            borderRadius: '6px',
                            border: 'none',
                            backgroundColor: isSelected ? `${themeColors.accent}15` : 'transparent',
                            cursor: 'pointer',
                            textAlign: 'left',
                            transition: TRANSITION.default,
                            marginBottom: '2px',
                          }}
                          onMouseEnter={(e) => {
                            if (!isSelected) {
                              e.currentTarget.style.backgroundColor = `${themeColors.accent}08`;
                            }
                          }}
                          onMouseLeave={(e) => {
                            if (!isSelected) {
                              e.currentTarget.style.backgroundColor = 'transparent';
                            }
                          }}
                        >
                          <div
                            style={{
                              flex: 1,
                              fontSize: '12px',
                              fontWeight: isSelected ? '600' : '500',
                              color: themeColors.text,
                              whiteSpace: 'nowrap',
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                            }}
                          >
                            {tool.name}
                          </div>
                          {isSelected && (
                            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke={themeColors.accent} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                              <polyline points="20 6 9 17 4 12" />
                            </svg>
                          )}
                        </button>
                      );
                    })}

                    {/* Refresh Tools Button inside dropdown */}
                    <div style={{
                      borderTop: `1px solid ${themeColors.border}`,
                      marginTop: '4px',
                      paddingTop: '4px',
                    }}>
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          handleRefreshTools();
                        }}
                        style={{
                          width: '100%',
                          padding: '6px 8px',
                          borderRadius: '6px',
                          border: 'none',
                          backgroundColor: 'transparent',
                          color: themeColors.textSecondary,
                          fontSize: '11px',
                          cursor: 'pointer',
                          transition: TRANSITION.default,
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          gap: '4px',
                        }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.backgroundColor = `${themeColors.accent}10`;
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.backgroundColor = 'transparent';
                        }}
                      >
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="rgba(33, 150, 243, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <polyline points="23 4 23 10 17 10" />
                          <polyline points="1 20 1 14 7 14" />
                          <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
                        </svg>
                        Refresh
                      </button>
                    </div>
                  </div>
                )}
              </>
            )}
          </div>}

          {/* Logout Section */}
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2px' }}>
            <div
            style={{
              fontSize: '9px',
              color: themeColors.textSecondary,
              fontWeight: '600',
              textTransform: 'uppercase',
              letterSpacing: '0.5px',
            }}
          >
            Logout
          </div>
          <button
            onClick={handleLogout}
            title="Logout"
            style={{
              width: '40px',
              height: '40px',
              borderRadius: '10px',
              border: '1px solid rgba(255, 255, 255, 0.1)',
              backgroundColor: 'transparent',
              cursor: 'pointer',
              fontSize: '18px',
              transition: TRANSITION.default,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: themeColors.text,
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.backgroundColor = '#ff444415';
              e.currentTarget.style.borderColor = '#ff4444';
              e.currentTarget.style.color = '#ff4444';
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.backgroundColor = 'transparent';
              e.currentTarget.style.borderColor = 'rgba(255, 255, 255, 0.1)';
              e.currentTarget.style.color = themeColors.text;
            }}
          >
            <div style={{ padding: '4px', border: '1px solid rgba(244, 67, 54, 0.3)', borderRadius: '6px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="rgba(244, 67, 54, 0.7)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
                <polyline points="16 17 21 12 16 7" />
                <line x1="21" y1="12" x2="9" y2="12" />
              </svg>
            </div>
          </button>
          </div>
        </div>
      </div>

      {/* Main content area with margins */}
      <div
        style={{
          flex: 1,
          display: 'flex',
          justifyContent: 'center',
          position: 'relative',
          marginLeft: '64px',
          marginRight: '64px',
          height: '100vh',
          overflow: 'hidden',
        }}
      >
        {/* Scrollable chat window */}
        <div
          id="main-scroll-container"
          className="main-scroll-container"
          style={{
            display: 'flex',
            flexDirection: 'column',
            width: '100%',
            maxWidth: '1100px',
            height: 'calc(100vh - 140px)',
            overflowY: 'auto',
            overflowX: 'hidden',
            paddingLeft: '32px',
            paddingRight: '32px',
            paddingTop: '0px',
            paddingBottom: '20px',
            scrollbarWidth: 'thin',
            scrollbarColor: `${themeColors.border} transparent`,
          }}
        >
          {/* Center content + input for first search only */}
          {state.messages.length === 0 && (
            <div style={{ minHeight: 'calc(100vh - 200px)', display: 'flex', flexDirection: 'column', justifyContent: 'center', alignItems: 'center' }}>
              <h1 style={{ margin: 0, marginBottom: '32px', fontSize: '32px', fontWeight: '600', textAlign: 'center' }}>
                Agentic Search
              </h1>
              <div style={{ width: '100%', maxWidth: '700px' }}>
                <InputArea />
              </div>
            </div>
          )}

          {/* Message list */}
          {state.messages.length > 0 && <MessageList />}
        </div>

        {/* Input area - fixed at bottom (only when conversation is active) */}
        {state.messages.length > 0 && (
        <div style={{
          position: 'fixed',
          bottom: '8px',
          left: '64px',
          right: '64px',
          display: 'flex',
          justifyContent: 'center',
          pointerEvents: 'none',
          zIndex: 100,
        }}>
          <div style={{
            width: '100%',
            maxWidth: '1100px',
            paddingLeft: '32px',
            paddingRight: '32px',
            pointerEvents: 'auto',
          }}>
            <InputArea />
          </div>
        </div>
        )}
      </div>

      {/* User info - fixed top right */}
      <div
        style={{
          position: 'fixed',
          right: '48px',
          top: '16px',
          textAlign: 'right',
          zIndex: 50,
        }}
      >
        <div
          style={{
            fontSize: '11px',
            fontWeight: '500',
            color: themeColors.text,
            marginBottom: '2px',
          }}
        >
          {state.user?.name || 'Loading...'}
        </div>
        <div
          style={{
            fontSize: '9px',
            color: themeColors.textSecondary,
          }}
        >
          {state.user?.email || ''}
        </div>
      </div>
    </div>
  );
}
