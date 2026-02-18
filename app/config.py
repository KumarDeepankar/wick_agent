from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    # LLM provider keys
    anthropic_api_key: str = ""
    openai_api_key: str = ""
    tavily_api_key: str = ""

    # Ollama (local models)
    ollama_base_url: str = "http://localhost:11434"

    # Gateway (OpenAI-compatible proxy: LiteLLM, vLLM, TGI, etc.)
    gateway_base_url: str = "http://localhost:4000"
    gateway_api_key: str = ""

    # Agent defaults
    default_model: str = "ollama:llama3.1:8b"
    default_backend: str = "state"
    default_debug: bool = False

    # Server
    host: str = "0.0.0.0"
    port: int = 8000

    model_config = {"env_file": ".env", "extra": "ignore"}


settings = Settings()
