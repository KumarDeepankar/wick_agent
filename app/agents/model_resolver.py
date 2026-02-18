"""Model resolver — translates model strings to LangChain model objects.

Supports multiple providers, local models, and LLMs behind gateways.

Model string formats:
    "claude-sonnet-4-5-20250929"              → framework default (Anthropic)
    "openai:gpt-4.1"                          → framework handles natively
    "google_genai:gemini-2.5-flash-lite"      → framework handles natively
    "ollama:llama3.1:8b"                      → local Ollama
    "ollama:mistral"                           → local Ollama
    "gateway:my-model-name"                   → OpenAI-compatible gateway/proxy

Gateway support:
    Any OpenAI-compatible API endpoint (LiteLLM, vLLM, TGI, Anyscale,
    Azure API Management, Kong, custom proxies). Set the base URL in
    models.yaml or the GATEWAY_BASE_URL environment variable.
"""

from __future__ import annotations

import logging
from typing import Any

from app.config import settings

logger = logging.getLogger(__name__)

# Providers that the deep-agents framework handles via init_chat_model
_FRAMEWORK_PROVIDERS = {
    "openai", "anthropic", "azure_openai",
    "google_genai", "bedrock_converse", "huggingface",
}


def resolve_model(model_input: str | dict[str, Any]) -> str | Any:
    """Resolve a model identifier to a string or LangChain model object.

    Args:
        model_input: Either a simple string ("ollama:llama3.1:8b") or a
                     dict with full config:
                     {
                         "provider": "ollama",
                         "model": "llama3.1:8b",
                         "base_url": "http://localhost:11434",
                         "api_key": "...",
                         "temperature": 0.7,
                         "max_tokens": 4096,
                     }
    """
    if isinstance(model_input, dict):
        return _resolve_from_dict(model_input)

    return _resolve_from_string(model_input)


def _resolve_from_string(model_str: str) -> str | Any:
    """Resolve a provider:model string."""
    if ":" not in model_str:
        return model_str

    # Split on first colon only — handles "ollama:llama3.1:8b"
    provider, model_name = model_str.split(":", 1)

    if provider in _FRAMEWORK_PROVIDERS:
        return model_str

    if provider == "ollama":
        return _create_ollama_model(model_name)

    if provider == "gateway":
        return _create_gateway_model(model_name)

    logger.warning("Unknown provider '%s', passing model string as-is", provider)
    return model_str


def _resolve_from_dict(cfg: dict[str, Any]) -> str | Any:
    """Resolve a full model config dict."""
    provider = cfg.get("provider", "")
    model_name = cfg.get("model", "")

    if not model_name:
        raise ValueError("Model config must include 'model' key")

    if provider in _FRAMEWORK_PROVIDERS:
        return f"{provider}:{model_name}"

    if provider == "ollama":
        return _create_ollama_model(
            model_name,
            base_url=cfg.get("base_url"),
            temperature=cfg.get("temperature"),
            max_tokens=cfg.get("max_tokens"),
        )

    if provider == "gateway":
        return _create_gateway_model(
            model_name,
            base_url=cfg.get("base_url"),
            api_key=cfg.get("api_key"),
            temperature=cfg.get("temperature"),
            max_tokens=cfg.get("max_tokens"),
        )

    # No provider or unknown — try as plain model string
    if provider:
        return f"{provider}:{model_name}"
    return model_name


# ═══════════════════════════════════════════════════════════════════════════
# Provider constructors
# ═══════════════════════════════════════════════════════════════════════════


def _create_ollama_model(
    model_name: str,
    *,
    base_url: str | None = None,
    temperature: float | None = None,
    max_tokens: int | None = None,
) -> Any:
    """Create a ChatOllama instance for local Ollama models."""
    from langchain_ollama import ChatOllama

    url = base_url or settings.ollama_base_url
    logger.info("Creating ChatOllama: model=%s, base_url=%s", model_name, url)

    kwargs: dict[str, Any] = {
        "model": model_name,
        "base_url": url,
    }
    if temperature is not None:
        kwargs["temperature"] = temperature
    if max_tokens is not None:
        kwargs["num_predict"] = max_tokens

    return ChatOllama(**kwargs)


def _create_gateway_model(
    model_name: str,
    *,
    base_url: str | None = None,
    api_key: str | None = None,
    temperature: float | None = None,
    max_tokens: int | None = None,
) -> Any:
    """Create a ChatOpenAI instance pointing at an OpenAI-compatible gateway.

    Works with: LiteLLM, vLLM, TGI, Anyscale, Azure APIM, Kong, etc.
    """
    from langchain_openai import ChatOpenAI

    url = base_url or settings.gateway_base_url
    key = api_key or settings.gateway_api_key

    logger.info("Creating gateway model: model=%s, base_url=%s", model_name, url)

    kwargs: dict[str, Any] = {
        "model": model_name,
        "base_url": url,
        "api_key": key or "not-needed",
    }
    if temperature is not None:
        kwargs["temperature"] = temperature
    if max_tokens is not None:
        kwargs["max_tokens"] = max_tokens

    return ChatOpenAI(**kwargs)
