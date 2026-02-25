import { useEffect, useState } from 'react';
import { ChatProvider } from './contexts/ChatContext';
import { ThemeProvider } from './contexts/ThemeContext';
import { ChatInterface } from './components/ChatInterface';
import { apiClient } from './services/api';
import { getBackendUrl } from './config';
import type { Tool } from './types';
import './styles/animations.css';

// Check for error params synchronously before component renders
function getInitialErrorState(): string | null {
  const urlParams = new URLSearchParams(window.location.search);
  const error = urlParams.get('error');
  const message = urlParams.get('message');

  if (error === 'access_denied') {
    // Clean up URL immediately
    window.history.replaceState({}, document.title, window.location.pathname);
    return message || 'Access denied. Please contact your administrator.';
  }
  return null;
}

function App() {
  // Initialize accessDenied synchronously from URL params
  const [accessDenied] = useState<string | null>(() => getInitialErrorState());
  const [tools, setTools] = useState<Tool[]>([]);
  const [isLoading, setIsLoading] = useState(!accessDenied); // Don't show loading if error
  const [authError, setAuthError] = useState(false);

  // Load tools on mount and subscribe to session events via SSE
  useEffect(() => {
    // Skip if access is already denied
    if (accessDenied) return;

    const loadTools = async () => {
      try {
        const fetchedTools = await apiClient.getTools();
        setTools(fetchedTools);
      } catch (error: any) {
        // Check if it's an authentication error
        if (error.message?.includes('401') || error.message?.includes('403') || error.message?.includes('Authentication required')) {
          // Clear cached data
          localStorage.removeItem('enabledTools');
          setAuthError(true);
          window.location.href = getBackendUrl('/auth/login');
        }
      } finally {
        setIsLoading(false);
      }
    };

    loadTools();

    // Subscribe to session events via SSE for real-time logout notifications
    const eventSource = new EventSource(getBackendUrl('/auth/session-events'), {
      withCredentials: true
    });

    // Flag to prevent multiple session checks on rapid SSE reconnection attempts
    let isCheckingSession = false;

    eventSource.addEventListener('logout', (event) => {
      console.log('Received logout event:', event.data);
      localStorage.removeItem('enabledTools');
      setAuthError(true);
      window.location.href = getBackendUrl('/auth/login?logout=1');
    });

    eventSource.addEventListener('heartbeat', () => {
      // Connection is alive, no action needed
    });

    eventSource.onerror = async () => {
      // Prevent multiple simultaneous session checks
      if (isCheckingSession) return;
      isCheckingSession = true;

      console.warn('SSE connection error - checking session validity');
      // SSE doesn't provide HTTP status, so we need to check if session is valid
      // by making a quick API call. This handles server restart scenarios.
      try {
        const response = await fetch(getBackendUrl('/tools'), {
          credentials: 'include'
        });
        if (response.status === 401 || response.status === 403) {
          // Session is invalid - redirect to login
          console.log('Session invalid after SSE error, redirecting to login');
          localStorage.removeItem('enabledTools');
          setAuthError(true);
          eventSource.close();
          window.location.href = getBackendUrl('/auth/login');
          return;
        }
        // If auth check passed, it's just a temporary network issue - SSE will auto-reconnect
      } catch (fetchError) {
        // Network error - SSE will auto-reconnect when network is back
        console.warn('Network error during session check:', fetchError);
      } finally {
        // Reset flag after a delay to allow for retry
        setTimeout(() => { isCheckingSession = false; }, 5000);
      }
    };

    return () => {
      eventSource.close();
    };
  }, [accessDenied]);

  // Initialize enabled tools
  useEffect(() => {
    if (tools.length > 0) {
      const enabledTools = tools.filter((t) => t.enabled).map((t) => t.name);
      // This will be used by ChatContext when initialized
      localStorage.setItem('enabledTools', JSON.stringify(enabledTools));
    }
  }, [tools]);

  if (accessDenied) {
    return (
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          height: '100vh',
          backgroundColor: '#0A1929',
          color: '#E7EBF0',
          fontSize: '18px',
          gap: '16px',
          padding: '20px',
          textAlign: 'center',
          animation: 'fadeIn 0.5s cubic-bezier(0.16, 1, 0.3, 1)',
        }}
      >
        <div style={{ fontSize: '48px', animation: 'scaleIn 0.6s cubic-bezier(0.16, 1, 0.3, 1)' }}>🚫</div>
        <div style={{ fontSize: '24px', fontWeight: 'bold', color: '#ff6b6b' }}>Authentication Failed!</div>
        <div style={{ fontSize: '16px', color: '#E7EBF0', marginTop: '8px' }}>Access Denied</div>
        <div style={{ fontSize: '14px', color: '#B2BAC2', maxWidth: '500px', marginTop: '8px' }}>
          {decodeURIComponent(accessDenied)}
        </div>
        <div style={{
          fontSize: '13px',
          color: '#90A4AE',
          maxWidth: '450px',
          marginTop: '16px',
          padding: '12px',
          backgroundColor: 'rgba(255,255,255,0.05)',
          borderRadius: '8px'
        }}>
          Your account does not have the required role mappings to access this application.
          Please contact your administrator to configure group-to-role mappings.
        </div>
        <div style={{ marginTop: '24px' }}>
          <button
            onClick={() => window.location.href = getBackendUrl('/auth/login?logout=1')}
            style={{
              padding: '12px 24px',
              backgroundColor: '#1976d2',
              color: 'white',
              border: 'none',
              borderRadius: '6px',
              cursor: 'pointer',
              fontSize: '14px',
              fontWeight: '500',
              transition: 'background-color 0.2s',
            }}
            onMouseOver={(e) => e.currentTarget.style.backgroundColor = '#1565c0'}
            onMouseOut={(e) => e.currentTarget.style.backgroundColor = '#1976d2'}
          >
            Try Again
          </button>
        </div>
      </div>
    );
  }

  if (authError) {
    return (
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          height: '100vh',
          backgroundColor: '#0A1929',
          color: '#E7EBF0',
          fontSize: '18px',
          gap: '16px',
          animation: 'fadeIn 0.5s cubic-bezier(0.16, 1, 0.3, 1)',
        }}
      >
        <div style={{ fontSize: '48px', animation: 'scaleIn 0.6s cubic-bezier(0.16, 1, 0.3, 1)' }}>🔐</div>
        <div>Authentication Required</div>
        <div style={{ fontSize: '14px', color: '#B2BAC2', animation: 'pulse 2s ease-in-out infinite' }}>
          Redirecting to login page...
        </div>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          height: '100vh',
          backgroundColor: '#0A1929',
          color: '#E7EBF0',
          fontSize: '18px',
          gap: '16px',
          animation: 'fadeIn 0.5s cubic-bezier(0.16, 1, 0.3, 1)',
        }}
      >
        <div style={{ fontSize: '48px', animation: 'scaleIn 0.6s cubic-bezier(0.16, 1, 0.3, 1)' }}>💬</div>
        <div style={{ animation: 'pulse 2s ease-in-out infinite' }}>
          Loading Agentic Search...
        </div>
      </div>
    );
  }

  return (
    <ChatProvider>
      <ThemeProvider>
        <ChatInterface />
      </ThemeProvider>
    </ChatProvider>
  );
}

export default App;
