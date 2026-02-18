# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2024-02-22

### Added

- `WickClient` — typed HTTP client for the wick_go agent server (health, CRUD, invoke, stream).
- `WickServer` — server lifecycle manager (build, start, stop, status, logs, context manager).
- `wick-agent` CLI with `build`, `start`, `stop`, `status`, `logs`, `systemd` subcommands.
- Typed message classes: `HumanMessage`, `AIMessage`, `SystemMessage`, `ToolMessage`.
- `Messages` chain with fluent builder, validation, filtering, and `+` operator support.
- Auto-start support in `WickClient` via `auto_start=True`.
- Go server source bundled under `server/`.
