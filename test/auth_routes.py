#!/usr/bin/env python3
"""
Authentication Routes for Agentic Search
Handles login, logout, and OAuth callbacks
"""
import os
import logging
from typing import Optional
from fastapi import APIRouter, Request, HTTPException, Response
from fastapi.responses import RedirectResponse, JSONResponse, HTMLResponse

from auth import (
    validate_jwt,
    create_session,
    delete_session,
    get_current_user,
    SESSION_COOKIE_NAME,
    SESSION_COOKIE_MAX_AGE
)

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/auth", tags=["authentication"])

# Service URLs from environment
# TOOLS_GATEWAY_URL: For server-to-server API calls (internal, HTTP)
# TOOLS_GATEWAY_PUBLIC_URL: For browser redirects (external, HTTPS via ALB)
TOOLS_GATEWAY_URL = os.getenv("TOOLS_GATEWAY_URL", "http://localhost:8021")
TOOLS_GATEWAY_PUBLIC_URL = os.getenv("TOOLS_GATEWAY_PUBLIC_URL", TOOLS_GATEWAY_URL)

# AGENTIC_SEARCH_URL: This service's public URL for OAuth callbacks
AGENTIC_SEARCH_URL = os.getenv("AGENTIC_SEARCH_URL", "http://localhost:8023")

# Auto SSO redirect: skip login page when only one OAuth provider is configured
AUTO_SSO_REDIRECT = os.getenv("AUTO_SSO_REDIRECT", "true").lower() in ("true", "1", "yes")


@router.get("/login")
async def login_page(error: Optional[str] = None, message: Optional[str] = None, logout: Optional[str] = None):
    """Render login page with OAuth options and local login"""
    # Auto-redirect to SSO if enabled, no errors/logout present, and exactly 1 provider
    if AUTO_SSO_REDIRECT and not error and not message and not logout:
        try:
            import aiohttp
            timeout = aiohttp.ClientTimeout(total=3)
            async with aiohttp.ClientSession(timeout=timeout) as session:
                async with session.get(f"{TOOLS_GATEWAY_URL}/auth/providers") as resp:
                    if resp.status == 200:
                        data = await resp.json()
                        providers = [p for p in (data.get("providers") or []) if p.get("enabled", True)]
                        if len(providers) == 1:
                            provider_id = providers[0]["provider_id"]
                            logger.info(f"Auto SSO redirect: single provider '{provider_id}', redirecting")
                            return RedirectResponse(url=f"/auth/oauth/{provider_id}")
        except Exception as e:
            logger.warning(f"Auto SSO redirect failed, showing login page: {e}")

    html = """
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Sign In - Agentic Search</title>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700&display=swap" rel="stylesheet">
    <link href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.0.0/css/all.min.css" rel="stylesheet">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }

        body {
            font-family: 'Inter', sans-serif;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            padding: 20px;
            background: #f8f9fb;
            color: #1a1a2e;
            position: relative;
            overflow: hidden;
        }

        .login-container {
            position: relative;
            z-index: 1;
            max-width: 420px;
            width: 100%;
            background: #ffffff;
            border-radius: 24px;
            border: none;
            box-shadow:
                0 1px 3px rgba(0,0,0,0.04),
                0 8px 32px rgba(0,0,0,0.06),
                0 24px 60px rgba(0,0,0,0.04);
            overflow: visible;
            animation: cardIn 0.7s cubic-bezier(0.16,1,0.3,1) forwards;
        }


        .login-header {
            padding: 48px 40px 0;
            text-align: center;
        }

        .login-header h1 {
            font-size: 22px;
            font-weight: 700;
            letter-spacing: 1.5px;
            text-transform: uppercase;
            color: #000000;
            margin-bottom: 8px;
            animation: textReveal 0.8s cubic-bezier(0.16,1,0.3,1) 0.3s backwards;
        }

        .login-header p {
            font-size: 13px;
            color: #8b95a5;
            letter-spacing: 0.5px;
            animation: subtitleIn 0.6s cubic-bezier(0.16,1,0.3,1) 0.5s backwards;
        }

        .login-body {
            padding: 36px 40px 40px;
            animation: formIn 0.6s cubic-bezier(0.16,1,0.3,1) 0.6s backwards;
        }

        .auth-section { margin-bottom: 0; }

        .auth-section-title {
            font-size: 11px;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 1.2px;
            color: #9ca3af;
            margin-bottom: 16px;
            display: flex;
            align-items: center;
            gap: 8px;
        }

        .auth-section-title i { font-size: 12px; }

        .form-group { margin-bottom: 20px; }

        .form-group label {
            display: block;
            font-size: 11px;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 1px;
            color: #6b7280;
            margin-bottom: 8px;
        }

        .form-group input {
            width: 100%;
            padding: 14px 16px;
            border: 1.5px solid #e5e7eb;
            border-radius: 12px;
            font-size: 14px;
            font-family: 'Inter', sans-serif;
            background: #fafbfc;
            color: #1a1a2e;
            transition: all 0.2s cubic-bezier(0.16,1,0.3,1);
        }

        .form-group input::placeholder {
            color: #c4c9d4;
        }

        .form-group input:focus {
            outline: none;
            border-color: #2563EB;
            background: #ffffff;
            box-shadow: 0 0 0 3px rgba(37,99,235,0.08);
        }

        .btn {
            width: 100%;
            padding: 14px;
            border: none;
            border-radius: 12px;
            font-size: 14px;
            font-weight: 600;
            cursor: pointer;
            font-family: 'Inter', sans-serif;
            transition: all 0.2s cubic-bezier(0.16,1,0.3,1);
            letter-spacing: 0.5px;
        }

        .btn-primary {
            background: #2563EB;
            color: white;
            box-shadow: 0 4px 16px rgba(37,99,235,0.25);
        }

        .btn-primary:hover {
            transform: translateY(-2px);
            box-shadow: 0 8px 24px rgba(37,99,235,0.35);
        }

        .btn-primary:active {
            transform: translateY(0);
            box-shadow: 0 2px 8px rgba(37,99,235,0.2);
        }

        .auth-divider {
            text-align: center;
            margin: 28px 0;
            position: relative;
            display: flex;
            align-items: center;
        }

        .auth-divider::before,
        .auth-divider::after {
            content: '';
            flex: 1;
            height: 1px;
        }

        .auth-divider::before {
            background: linear-gradient(90deg, transparent, #e5e7eb);
        }

        .auth-divider::after {
            background: linear-gradient(90deg, #e5e7eb, transparent);
        }

        .auth-divider span {
            padding: 0 16px;
            color: #b0b8c4;
            font-size: 11px;
            font-weight: 600;
            letter-spacing: 2px;
            text-transform: uppercase;
        }

        .oauth-providers-list {
            display: flex;
            flex-direction: column;
            gap: 10px;
        }

        .oauth-provider-btn {
            display: flex;
            align-items: center;
            justify-content: center;
            gap: 10px;
            padding: 13px 20px;
            border: 1.5px solid #e5e7eb;
            border-radius: 12px;
            background: #ffffff;
            color: #374151;
            font-weight: 600;
            font-size: 14px;
            cursor: pointer;
            transition: all 0.2s cubic-bezier(0.16,1,0.3,1);
            font-family: 'Inter', sans-serif;
            letter-spacing: 0.3px;
        }

        .oauth-provider-btn:hover {
            border-color: #2563EB;
            background: #f8faff;
            transform: translateY(-2px);
            box-shadow: 0 4px 16px rgba(37,99,235,0.1);
        }

        .oauth-provider-btn:active {
            transform: translateY(0);
        }

        .oauth-provider-btn i { font-size: 16px; }
        .oauth-provider-btn.google i { color: #4285F4; }
        .oauth-provider-btn.microsoft i { color: #00A4EF; }
        .oauth-provider-btn.github i { color: #24292e; }

        .alert {
            padding: 12px 16px;
            border-radius: 12px;
            margin-bottom: 20px;
            font-size: 13px;
            display: none;
        }

        .alert-danger {
            background: #fef2f2;
            color: #dc2626;
            border: 1px solid #fecaca;
        }

        .alert-info {
            background: #eff6ff;
            color: #2563EB;
            border: 1px solid #bfdbfe;
        }

        .login-footer {
            text-align: center;
            padding: 0 40px 28px;
            font-size: 11px;
            color: #c4c9d4;
            letter-spacing: 0.5px;
        }

        /* Spinner */
        .spinner {
            display: inline-block;
            width: 16px; height: 16px;
            border: 2px solid rgba(255,255,255,0.3);
            border-top-color: white;
            border-radius: 50%;
            animation: spin 0.8s linear infinite;
            vertical-align: middle;
        }

        .btn:disabled {
            opacity: 0.5;
            cursor: not-allowed;
            transform: none !important;
            box-shadow: none !important;
        }

        /* Animations */
        @keyframes cardIn {
            from { opacity: 0; transform: translateY(32px) scale(0.96); }
            to { opacity: 1; transform: translateY(0) scale(1); }
        }
        @keyframes textReveal {
            from { opacity: 0; transform: translateY(12px); letter-spacing: 6px; }
            to { opacity: 1; transform: translateY(0); letter-spacing: 1.5px; }
        }
        @keyframes subtitleIn {
            from { opacity: 0; transform: translateY(8px); }
            to { opacity: 1; transform: translateY(0); }
        }
        @keyframes formIn {
            from { opacity: 0; transform: translateY(16px); }
            to { opacity: 1; transform: translateY(0); }
        }
        @keyframes spin {
            to { transform: rotate(360deg); }
        }

        /* Responsive */
        @media (max-width: 480px) {
            .login-header { padding: 36px 24px 0; }
            .login-body { padding: 28px 24px 32px; }
            .login-footer { padding: 0 24px 24px; }
            .login-container { border-radius: 20px; }
        }
    </style>
</head>
<body>
    <div class="login-container">
        <!-- Header -->
        <div class="login-header">
            <h1>Agentic Search</h1>
        </div>

        <!-- Body -->
        <div class="login-body">
            <!-- OAuth Login Section -->
            <div class="auth-section" id="oauthSection" style="display: none;">
                <div class="oauth-providers-list" id="oauthProviderButtons">
                    <!-- OAuth buttons will be inserted here -->
                </div>
            </div>

            <!-- Divider -->
            <div class="auth-divider" id="authDivider" style="display: none;">
                <span>or</span>
            </div>

            <!-- Local Login Section -->
            <div class="auth-section" id="localSection">
                <!-- Error Message -->
                <div class="alert alert-danger" id="loginError"></div>

                <!-- Login Form -->
                <form id="localLoginForm" onsubmit="return handleLocalLogin(event);">
                    <div class="form-group">
                        <label for="localEmail">Username or Email</label>
                        <input type="text" id="localEmail" name="email" required autocomplete="username">
                    </div>
                    <div class="form-group">
                        <label for="localPassword">Password</label>
                        <input type="password" id="localPassword" name="password" required autocomplete="current-password">
                    </div>
                    <button type="submit" class="btn btn-primary" id="loginButton">
                        Sign In
                    </button>
                </form>
            </div>

            <!-- No Providers Message -->
            <div class="alert alert-info" id="noProvidersMessage" style="display: none;">
                No authentication providers configured. Please contact your administrator.
            </div>
        </div>

    </div>

    <script>
        let oauthProviders = [];
        let isSubmitting = false;

        // Load OAuth providers on page load and check for errors
        document.addEventListener('DOMContentLoaded', function() {
            checkForErrors();
            loadOAuthProviders();
        });

        /**
         * Check URL for error parameters and display them
         */
        function checkForErrors() {
            const urlParams = new URLSearchParams(window.location.search);
            const error = urlParams.get('error');
            const message = urlParams.get('message');

            if (error || message) {
                const errorDiv = document.getElementById('loginError');
                let errorText = '';

                if (error === 'access_denied') {
                    errorText = '🚫 Authentication Failed! ';
                }

                if (message) {
                    errorText += decodeURIComponent(message);
                } else if (error) {
                    errorText += error.replace(/_/g, ' ');
                }

                errorDiv.innerHTML = errorText;
                errorDiv.style.display = 'block';

                // Clean up URL
                window.history.replaceState({}, document.title, window.location.pathname);
            }
        }

        /**
         * Load available OAuth providers
         */
        async function loadOAuthProviders() {
            try {
                const response = await fetch('/auth/providers');
                if (response.ok) {
                    const data = await response.json();
                    oauthProviders = data.providers || [];
                    renderOAuthProviders();
                }
            } catch (error) {
                console.error('Failed to load OAuth providers:', error);
            }
        }

        /**
         * Render OAuth provider buttons
         */
        function renderOAuthProviders() {
            const oauthSection = document.getElementById('oauthSection');
            const localSection = document.getElementById('localSection');
            const divider = document.getElementById('authDivider');
            const container = document.getElementById('oauthProviderButtons');
            const noProvidersMsg = document.getElementById('noProvidersMessage');

            if (oauthProviders.length === 0) {
                oauthSection.style.display = 'none';
                divider.style.display = 'none';
                localSection.style.display = 'block';
                return;
            }

            oauthSection.style.display = 'block';
            divider.style.display = 'block';
            localSection.style.display = 'block';
            noProvidersMsg.style.display = 'none';

            const providerIcons = {
                'google': 'fab fa-google',
                'microsoft': 'fab fa-microsoft',
                'github': 'fab fa-github'
            };

            container.innerHTML = oauthProviders.map(provider => {
                const icon = providerIcons[provider.provider_id] || 'fas fa-sign-in-alt';
                const className = provider.provider_id.toLowerCase();

                return `
                    <button class="oauth-provider-btn ${className}" onclick="initiateOAuthLogin('${provider.provider_id}')">
                        <i class="${icon}"></i>
                        Sign in with ${provider.provider_name}
                    </button>
                `;
            }).join('');
        }

        /**
         * Handle local login form submission
         */
        async function handleLocalLogin(event) {
            event.preventDefault();

            if (isSubmitting) return false;
            isSubmitting = true;

            const email = document.getElementById('localEmail').value;
            const password = document.getElementById('localPassword').value;
            const errorDiv = document.getElementById('loginError');
            const loginButton = document.getElementById('loginButton');

            // Show loading state
            loginButton.disabled = true;
            loginButton.innerHTML = '<span class="spinner"></span> Signing in...';
            errorDiv.style.display = 'none';

            try {
                const response = await fetch('/auth/login/local', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json'
                    },
                    body: JSON.stringify({ email, password })
                });

                if (response.ok) {
                    const data = await response.json();

                    // Redirect to main page (cookie is set automatically)
                    window.location.href = '/';
                } else {
                    const error = await response.json();
                    errorDiv.textContent = error.detail || 'Invalid credentials';
                    errorDiv.style.display = 'block';

                    // Reset button
                    loginButton.disabled = false;
                    loginButton.innerHTML = 'Sign In';
                    isSubmitting = false;
                }
            } catch (error) {
                console.error('Login failed:', error);
                errorDiv.textContent = 'Login failed. Please try again.';
                errorDiv.style.display = 'block';

                // Reset button
                loginButton.disabled = false;
                loginButton.innerHTML = 'Sign In';
                isSubmitting = false;
            }

            return false;
        }

        /**
         * Initiate OAuth login flow
         */
        function initiateOAuthLogin(providerId) {
            // Redirect to OAuth endpoint
            window.location.href = `/auth/oauth/${providerId}`;
        }
    </script>
</body>
</html>
    """
    return HTMLResponse(content=html)


@router.get("/oauth/{provider_id}")
async def oauth_login(provider_id: str):
    """
    Initiate OAuth login flow via tools_gateway.
    Redirects user's BROWSER to tools_gateway for authentication.
    """
    # Build callback URL for this service
    callback_url = f"{AGENTIC_SEARCH_URL}/auth/callback"

    # Redirect to tools_gateway OAuth with redirect_to parameter
    login_url = f"{TOOLS_GATEWAY_PUBLIC_URL}/auth/login-redirect?provider_id={provider_id}&redirect_to={callback_url}"

    logger.info(f"Initiating OAuth login for provider: {provider_id}")
    logger.info(f"Redirecting to: {login_url}")
    return RedirectResponse(url=login_url)


@router.get("/callback")
async def oauth_callback(
    response: Response,
    token: Optional[str] = None,
    error: Optional[str] = None,
    message: Optional[str] = None
):
    """
    OAuth callback after successful authentication.
    Receives JWT token from tools_gateway and creates session.
    Also handles error redirects (e.g., access denied due to no role mapping).
    """
    logger.info(f"OAuth callback received - token: {bool(token)}, error: {error}, message: {message}")

    try:
        # Check for error redirect (e.g., no role mapping)
        if error:
            logger.warning(f"OAuth callback received error: {error}, message: {message}")
            # Redirect to login page with error parameters
            # URL-encode the message since FastAPI decoded it
            from urllib.parse import quote
            error_url = f"/auth/login?error={error}"
            if message:
                encoded_message = quote(message, safe='')
                error_url += f"&message={encoded_message}"
            logger.info(f"Redirecting to login page with error: {error_url}")
            return RedirectResponse(url=error_url, status_code=302)

        # Token is required for successful auth
        if not token:
            logger.error("No token provided in OAuth callback")
            raise HTTPException(status_code=400, detail="Authentication token required")

        # Validate the JWT token
        payload = validate_jwt(token)
        if not payload:
            logger.error("Invalid JWT token received in callback")
            raise HTTPException(status_code=401, detail="Invalid authentication token")

        # Extract user data from token
        user_data = {
            "email": payload.get("email"),
            "name": payload.get("name"),
            "sub": payload.get("sub"),
            "provider": payload.get("provider")
        }

        logger.info(f"User authenticated: {user_data.get('email')} via {user_data.get('provider')}")

        # Create session
        session_id = create_session(user_data, token)

        # Set session cookie and redirect to main app
        redirect_response = RedirectResponse(url="/", status_code=302)
        redirect_response.set_cookie(
            key=SESSION_COOKIE_NAME,
            value=session_id,
            max_age=SESSION_COOKIE_MAX_AGE,
            httponly=True,
            secure=os.getenv("SESSION_COOKIE_SECURE", "false").lower() == "true",
            samesite=os.getenv("SESSION_COOKIE_SAMESITE", "lax")
        )

        return redirect_response

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"OAuth callback error: {e}")
        raise HTTPException(status_code=500, detail=f"Authentication failed: {str(e)}")


@router.get("/user")
async def get_user_info(request: Request):
    """Get current user info"""
    user = get_current_user(request)
    if not user:
        raise HTTPException(status_code=401, detail="Not authenticated")

    # Remove JWT token from response for security
    user_info = {k: v for k, v in user.items() if k != "jwt_token"}

    return JSONResponse(content=user_info)


@router.post("/logout")
async def logout(request: Request, response: Response):
    """Logout user and clear session"""
    session_id = request.cookies.get(SESSION_COOKIE_NAME)
    if session_id:
        delete_session(session_id)
        logger.info(f"User logged out: session {session_id[:8]}...")

    # Clear cookie and return response
    logout_response = JSONResponse(content={"message": "Logged out successfully"})
    logout_response.delete_cookie(SESSION_COOKIE_NAME)

    return logout_response


@router.get("/status")
async def auth_status(request: Request):
    """Check authentication status"""
    user = get_current_user(request)

    if user:
        return JSONResponse(content={
            "authenticated": True,
            "user": {
                "email": user.get("email"),
                "name": user.get("name"),
                "provider": user.get("provider")
            }
        })
    else:
        return JSONResponse(content={
            "authenticated": False
        })


@router.get("/providers")
async def get_auth_providers():
    """
    Get available OAuth providers from tools_gateway.
    Returns list of configured OAuth providers.
    """
    try:
        import aiohttp
        async with aiohttp.ClientSession() as session:
            async with session.get(f"{TOOLS_GATEWAY_URL}/auth/providers") as response:
                if response.status == 200:
                    data = await response.json()
                    return JSONResponse(content=data)
                else:
                    logger.error(f"Failed to fetch providers from tools_gateway: {response.status}")
                    return JSONResponse(content={"providers": []})
    except Exception as e:
        logger.error(f"Error fetching auth providers: {e}")
        return JSONResponse(content={"providers": []})


@router.post("/login/local")
async def local_login(request: Request, response: Response):
    """
    Local login via tools_gateway.
    Forwards credentials to tools_gateway for authentication.
    """
    try:
        body = await request.json()
        email = body.get("email")
        password = body.get("password")

        if not email or not password:
            raise HTTPException(status_code=400, detail="Email and password required")

        # Forward login request to tools_gateway
        import aiohttp
        async with aiohttp.ClientSession() as session:
            async with session.post(
                f"{TOOLS_GATEWAY_URL}/auth/login/local",
                json={"email": email, "password": password}
            ) as resp:
                if resp.status == 200:
                    data = await resp.json()
                    token = data.get("access_token")

                    if not token:
                        raise HTTPException(status_code=500, detail="No token received from authentication service")

                    # Validate the JWT token
                    payload = validate_jwt(token)
                    if not payload:
                        logger.error("Invalid JWT token received from tools_gateway")
                        raise HTTPException(status_code=401, detail="Invalid authentication token")

                    # Extract user data from token
                    user_data = {
                        "email": payload.get("email"),
                        "name": payload.get("name"),
                        "sub": payload.get("sub"),
                        "provider": payload.get("provider", "local")
                    }

                    logger.info(f"User authenticated locally: {user_data.get('email')}")

                    # Create session
                    session_id = create_session(user_data, token)

                    # Set session cookie
                    login_response = JSONResponse(content={
                        "success": True,
                        "access_token": token
                    })
                    login_response.set_cookie(
                        key=SESSION_COOKIE_NAME,
                        value=session_id,
                        max_age=SESSION_COOKIE_MAX_AGE,
                        httponly=True,
                        secure=os.getenv("SESSION_COOKIE_SECURE", "false").lower() == "true",
                        samesite=os.getenv("SESSION_COOKIE_SAMESITE", "lax")
                    )

                    return login_response
                else:
                    error_data = await resp.json()
                    raise HTTPException(status_code=resp.status, detail=error_data.get("detail", "Authentication failed"))

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Local login error: {e}")
        raise HTTPException(status_code=500, detail=f"Authentication failed: {str(e)}")
