import { useState, useEffect } from 'react';
import { apiClient } from '../services/api';
import type { AuthProvider } from '../types';
import { TRANSITION, EASING, DURATION } from '../styles/animations';

interface LoginPageProps {
  onLoginSuccess: () => void;
}

// Inject keyframes once
const styleId = 'login-page-keyframes';
if (typeof document !== 'undefined' && !document.getElementById(styleId)) {
  const style = document.createElement('style');
  style.id = styleId;
  style.textContent = `
    @keyframes loginGradientShift {
      0%, 100% { background-position: 0% 50%; }
      50% { background-position: 100% 50%; }
    }
    @keyframes loginFloat {
      0%, 100% { transform: translateY(0px) rotate(0deg); opacity: 0.12; }
      50% { transform: translateY(-20px) rotate(5deg); opacity: 0.2; }
    }
    @keyframes loginCardIn {
      from { opacity: 0; transform: translateY(32px) scale(0.96); }
      to { opacity: 1; transform: translateY(0) scale(1); }
    }
    @keyframes loginPulseRing {
      0% { transform: scale(0.9); opacity: 0.4; }
      50% { transform: scale(1.15); opacity: 0; }
      100% { transform: scale(0.9); opacity: 0; }
    }
    @keyframes loginTextReveal {
      from { opacity: 0; transform: translateY(12px); letter-spacing: 6px; }
      to { opacity: 1; transform: translateY(0); letter-spacing: 1.5px; }
    }
    @keyframes loginSubtitleIn {
      from { opacity: 0; transform: translateY(8px); }
      to { opacity: 1; transform: translateY(0); }
    }
    @keyframes loginFormIn {
      from { opacity: 0; transform: translateY(16px); }
      to { opacity: 1; transform: translateY(0); }
    }
    @keyframes loginOrbMove1 {
      0%, 100% { transform: translate(0, 0) scale(1); }
      33% { transform: translate(30px, -50px) scale(1.1); }
      66% { transform: translate(-20px, 20px) scale(0.9); }
    }
    @keyframes loginOrbMove2 {
      0%, 100% { transform: translate(0, 0) scale(1); }
      33% { transform: translate(-40px, 30px) scale(0.95); }
      66% { transform: translate(20px, -40px) scale(1.05); }
    }
  `;
  document.head.appendChild(style);
}

export function LoginPage({ onLoginSuccess }: LoginPageProps) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [providers, setProviders] = useState<AuthProvider[]>([]);
  const [loadingProviders, setLoadingProviders] = useState(true);
  const [focusedField, setFocusedField] = useState<string | null>(null);

  useEffect(() => {
    const loadProviders = async () => {
      try {
        const fetchedProviders = await apiClient.getAuthProviders();
        setProviders(fetchedProviders.filter(p => p.enabled));
      } catch (err) {
        // Continue — local auth should still work
      } finally {
        setLoadingProviders(false);
      }
    };
    loadProviders();
  }, []);

  const handleLocalLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setIsLoading(true);

    try {
      await apiClient.loginLocal(email, password);
      onLoginSuccess();
    } catch (err: any) {
      let errorMessage = 'Login failed';
      if (err.message.includes('Invalid credentials') || err.message.includes('401')) {
        errorMessage = 'Invalid username/email or password';
      } else if (err.message.includes('Network') || err.message.includes('fetch')) {
        errorMessage = 'Cannot connect to server. Please check if the service is running.';
      } else {
        errorMessage = err.message || 'Login failed';
      }
      setError(errorMessage);
    } finally {
      setIsLoading(false);
    }
  };

  const handleOAuthLogin = (providerId: string) => {
    apiClient.loginOAuth(providerId);
  };

  const getProviderIcon = (providerId: string): string => {
    const icons: Record<string, string> = {
      google: 'fab fa-google',
      microsoft: 'fab fa-microsoft',
      github: 'fab fa-github',
      azure: 'fab fa-microsoft',
      okta: 'fas fa-key',
    };
    return icons[providerId.toLowerCase()] || 'fas fa-sign-in-alt';
  };

  const getProviderColor = (providerId: string): string => {
    const colors: Record<string, string> = {
      google: '#4285F4',
      microsoft: '#00A4EF',
      github: '#24292e',
      azure: '#0078D4',
      okta: '#007DC1',
    };
    return colors[providerId.toLowerCase()] || '#667eea';
  };

  const inputStyle = (field: string): React.CSSProperties => ({
    width: '100%',
    padding: '14px 16px',
    fontSize: '14px',
    fontFamily: 'inherit',
    border: `1.5px solid ${focusedField === field ? 'rgba(99, 179, 255, 0.6)' : 'rgba(255, 255, 255, 0.1)'}`,
    borderRadius: '12px',
    background: focusedField === field ? 'rgba(255, 255, 255, 0.08)' : 'rgba(255, 255, 255, 0.04)',
    color: '#F0F4F8',
    outline: 'none',
    transition: `all ${DURATION.normal} ${EASING.standard}`,
    boxShadow: focusedField === field ? '0 0 0 3px rgba(99, 179, 255, 0.1)' : 'none',
    letterSpacing: field === 'password' && password && focusedField !== field ? '3px' : 'normal',
  });

  const labelStyle: React.CSSProperties = {
    display: 'block',
    marginBottom: '8px',
    fontSize: '12px',
    fontWeight: 600,
    textTransform: 'uppercase',
    letterSpacing: '1px',
    color: 'rgba(179, 197, 219, 0.8)',
  };

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        minHeight: '100vh',
        background: 'linear-gradient(135deg, #050d1a 0%, #0a1628 25%, #0f1d35 50%, #0a1628 75%, #050d1a 100%)',
        backgroundSize: '400% 400%',
        animation: 'loginGradientShift 15s ease infinite',
        color: '#E7EBF0',
        padding: '20px',
        position: 'relative',
        overflow: 'hidden',
        fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif",
      }}
    >
      {/* Ambient orbs */}
      <div style={{
        position: 'absolute',
        top: '15%',
        left: '20%',
        width: '350px',
        height: '350px',
        borderRadius: '50%',
        background: 'radial-gradient(circle, rgba(33, 150, 243, 0.08) 0%, transparent 70%)',
        animation: 'loginOrbMove1 20s ease-in-out infinite',
        pointerEvents: 'none',
      }} />
      <div style={{
        position: 'absolute',
        bottom: '10%',
        right: '15%',
        width: '300px',
        height: '300px',
        borderRadius: '50%',
        background: 'radial-gradient(circle, rgba(103, 58, 183, 0.07) 0%, transparent 70%)',
        animation: 'loginOrbMove2 25s ease-in-out infinite',
        pointerEvents: 'none',
      }} />

      {/* Subtle grid overlay */}
      <div style={{
        position: 'absolute',
        inset: 0,
        backgroundImage: `
          linear-gradient(rgba(255,255,255,0.015) 1px, transparent 1px),
          linear-gradient(90deg, rgba(255,255,255,0.015) 1px, transparent 1px)
        `,
        backgroundSize: '60px 60px',
        pointerEvents: 'none',
      }} />

      {/* Login card */}
      <div
        style={{
          width: '100%',
          maxWidth: '420px',
          background: 'rgba(255, 255, 255, 0.03)',
          backdropFilter: 'blur(24px) saturate(1.2)',
          WebkitBackdropFilter: 'blur(24px) saturate(1.2)',
          borderRadius: '24px',
          padding: '48px 40px 40px',
          boxShadow: `
            0 0 0 1px rgba(255, 255, 255, 0.06),
            0 1px 2px rgba(0, 0, 0, 0.3),
            0 8px 40px rgba(0, 0, 0, 0.4),
            0 24px 80px rgba(0, 0, 0, 0.2)
          `,
          position: 'relative',
          zIndex: 1,
          animation: 'loginCardIn 0.7s cubic-bezier(0.16, 1, 0.3, 1) forwards',
        }}
      >

        <div style={{ textAlign: 'center', marginBottom: '36px' }}>
          <h1 style={{
            margin: 0,
            fontSize: '24px',
            fontWeight: 700,
            letterSpacing: '1.5px',
            textTransform: 'uppercase',
            color: '#000000',
            animation: 'loginTextReveal 0.8s cubic-bezier(0.16, 1, 0.3, 1) 0.3s backwards',
          }}>
            Agentic Search
          </h1>
          <p style={{
            margin: '10px 0 0',
            color: 'rgba(178, 197, 219, 0.7)',
            fontSize: '13px',
            fontWeight: 400,
            letterSpacing: '0.5px',
            animation: 'loginSubtitleIn 0.6s cubic-bezier(0.16, 1, 0.3, 1) 0.5s backwards',
          }}>
            AI-powered research assistant
          </p>
        </div>

        {/* OAuth Providers */}
        {!loadingProviders && providers.length > 0 && (
          <div style={{
            marginBottom: '28px',
            animation: 'loginFormIn 0.6s cubic-bezier(0.16, 1, 0.3, 1) 0.55s backwards',
          }}>
            {providers.map((provider) => (
              <button
                key={provider.provider_id}
                onClick={() => handleOAuthLogin(provider.provider_id)}
                style={{
                  width: '100%',
                  padding: '13px 16px',
                  marginBottom: '10px',
                  fontSize: '14px',
                  fontWeight: 600,
                  fontFamily: 'inherit',
                  color: '#fff',
                  background: getProviderColor(provider.provider_id),
                  border: 'none',
                  borderRadius: '12px',
                  cursor: 'pointer',
                  transition: TRANSITION.default,
                  boxShadow: `0 2px 8px ${getProviderColor(provider.provider_id)}33`,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: '10px',
                  letterSpacing: '0.3px',
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.transform = 'translateY(-2px)';
                  e.currentTarget.style.boxShadow = `0 6px 20px ${getProviderColor(provider.provider_id)}44`;
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.transform = 'translateY(0)';
                  e.currentTarget.style.boxShadow = `0 2px 8px ${getProviderColor(provider.provider_id)}33`;
                }}
              >
                <i className={getProviderIcon(provider.provider_id)} style={{ fontSize: '16px' }}></i>
                Continue with {provider.provider_name}
              </button>
            ))}

            <div style={{
              display: 'flex',
              alignItems: 'center',
              margin: '24px 0',
              color: 'rgba(178, 197, 219, 0.4)',
              fontSize: '11px',
              fontWeight: 600,
              letterSpacing: '2px',
              textTransform: 'uppercase',
            }}>
              <div style={{ flex: 1, height: '1px', background: 'linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.1))' }} />
              <span style={{ padding: '0 16px' }}>or</span>
              <div style={{ flex: 1, height: '1px', background: 'linear-gradient(90deg, rgba(255, 255, 255, 0.1), transparent)' }} />
            </div>
          </div>
        )}

        {/* Login form */}
        <form
          onSubmit={handleLocalLogin}
          style={{
            animation: 'loginFormIn 0.6s cubic-bezier(0.16, 1, 0.3, 1) 0.65s backwards',
          }}
        >
          <div style={{ marginBottom: '20px' }}>
            <label htmlFor="email" style={labelStyle}>
              Username or Email
            </label>
            <input
              id="email"
              type="text"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              autoComplete="username"
              placeholder="you@company.com"
              style={inputStyle('email')}
              onFocus={() => setFocusedField('email')}
              onBlur={() => setFocusedField(null)}
            />
          </div>

          <div style={{ marginBottom: '28px' }}>
            <label htmlFor="password" style={labelStyle}>
              Password
            </label>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              autoComplete="current-password"
              placeholder="Enter your password"
              style={inputStyle('password')}
              onFocus={() => setFocusedField('password')}
              onBlur={() => setFocusedField(null)}
            />
          </div>

          {error && (
            <div
              style={{
                padding: '12px 16px',
                marginBottom: '20px',
                background: 'rgba(239, 68, 68, 0.08)',
                border: '1px solid rgba(239, 68, 68, 0.2)',
                borderRadius: '12px',
                color: '#FCA5A5',
                fontSize: '13px',
                display: 'flex',
                alignItems: 'center',
                gap: '10px',
                animation: 'loginFormIn 0.3s cubic-bezier(0.16, 1, 0.3, 1)',
              }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                <circle cx="12" cy="12" r="10" />
                <line x1="15" y1="9" x2="9" y2="15" />
                <line x1="9" y1="9" x2="15" y2="15" />
              </svg>
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={isLoading}
            style={{
              width: '100%',
              padding: '14px',
              fontSize: '14px',
              fontWeight: 600,
              fontFamily: 'inherit',
              color: '#fff',
              background: isLoading
                ? 'rgba(99, 179, 255, 0.3)'
                : 'linear-gradient(135deg, #2563EB 0%, #4F46E5 100%)',
              border: 'none',
              borderRadius: '12px',
              cursor: isLoading ? 'not-allowed' : 'pointer',
              transition: `all ${DURATION.normal} ${EASING.standard}`,
              boxShadow: isLoading
                ? 'none'
                : '0 4px 16px rgba(37, 99, 235, 0.3)',
              letterSpacing: '0.5px',
              position: 'relative',
              overflow: 'hidden',
            }}
            onMouseEnter={(e) => {
              if (!isLoading) {
                e.currentTarget.style.transform = 'translateY(-2px)';
                e.currentTarget.style.boxShadow = '0 8px 24px rgba(37, 99, 235, 0.4)';
              }
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.transform = 'translateY(0)';
              e.currentTarget.style.boxShadow = '0 4px 16px rgba(37, 99, 235, 0.3)';
            }}
          >
            {isLoading ? (
              <span style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: '10px' }}>
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" style={{ animation: 'spin 0.8s linear infinite' }}>
                  <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" />
                </svg>
                Signing in...
              </span>
            ) : (
              'Sign In'
            )}
          </button>
        </form>

        {providers.length === 0 && !loadingProviders && (
          <div
            style={{
              marginTop: '24px',
              padding: '14px 16px',
              background: 'rgba(99, 179, 255, 0.06)',
              border: '1px solid rgba(99, 179, 255, 0.1)',
              borderRadius: '12px',
              fontSize: '12px',
              color: 'rgba(178, 197, 219, 0.6)',
              animation: 'loginFormIn 0.6s cubic-bezier(0.16, 1, 0.3, 1) 0.8s backwards',
            }}
          >
            <span style={{ color: 'rgba(99, 179, 255, 0.8)', fontWeight: 600 }}>Local mode</span>
            {' '}&mdash; No SSO providers configured. Using email & password.
          </div>
        )}
      </div>

      {/* Footer */}
      <div style={{
        position: 'absolute',
        bottom: '24px',
        left: 0,
        right: 0,
        textAlign: 'center',
        fontSize: '11px',
        color: 'rgba(178, 197, 219, 0.25)',
        letterSpacing: '0.5px',
      }}>
        Agentic Search
      </div>
    </div>
  );
}
