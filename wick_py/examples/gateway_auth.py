"""Gateway token provider.

Replace the body of `fetch_token()` with your actual token endpoint logic.
"""

import httpx

TOKEN_URL = "https://my-gateway.com/oauth/token"


def fetch_token() -> str:
    """Call the token endpoint and return an access token."""
    resp = httpx.post(TOKEN_URL, data={
        "grant_type": "client_credentials",
        # "client_id": "...",
        # "client_secret": "...",
    })
    resp.raise_for_status()
    return resp.json()["access_token"]
