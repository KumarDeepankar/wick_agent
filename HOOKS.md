# Hooks System

The hooks system provides agent middleware using an **onion ring pattern**. Each hook implements the `Hook` interface (`agent/hook.go`) and can participate in 5 phases of the agent lifecycle. Hooks are composed in order — each wraps the next, forming layered middleware.

## Hook Phases

The agent loop calls hooks at 5 distinct points. The execution order follows the agent lifecycle:

```
┌─────────────────────────────────────────────────────────────────┐
│                        Agent Run                                │
│                                                                 │
│  1. BeforeAgent  (once)                                         │
│     ↓                                                           │
│  ┌─────────────── Agent Loop (max 25 iterations) ─────────────┐ │
│  │                                                             │ │
│  │  2. ModifyRequest  ──→  system prompt + messages modified   │ │
│  │     ↓                                                       │ │
│  │  3. WrapModelCall  ──→  LLM call (onion ring)               │ │
│  │     ↓                                                       │ │
│  │  4. AfterModel     ──→  inspect/intercept tool calls        │ │
│  │     ↓                                                       │ │
│  │  5. WrapToolCall   ──→  each tool execution (onion ring)    │ │
│  │     ↓                                                       │ │
│  │  (loop back to 2 if model requested tool calls)             │ │
│  └─────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

### Phase Details

| # | Phase | When | Signature | Purpose |
|---|-------|------|-----------|---------|
| 1 | `BeforeAgent` | Once before the loop starts | `(ctx, *AgentState) → error` | One-time setup: register tools, load data, initialize state |
| 2 | `ModifyRequest` | Before each LLM call | `(ctx, systemPrompt, []Message) → (systemPrompt, []Message, error)` | Modify system prompt and/or message list before sending to the LLM |
| 3 | `WrapModelCall` | Around each LLM call | `(ctx, []Message, next) → (*Response, error)` | Wrap/intercept the model invocation (onion ring — call `next()` to proceed) |
| 4 | `AfterModel` | After LLM responds, before tool dispatch | `(ctx, *AgentState, []ToolCall) → (map[id]ToolResult, error)` | Inspect tool calls; return pre-built results to intercept/reject specific calls |
| 5 | `WrapToolCall` | Around each tool execution | `(ctx, ToolCall, next) → (*ToolResult, error)` | Wrap individual tool calls (onion ring — call `next()` to proceed) |

## The 7 Hooks

### 1. FilesystemHook (`hooks/filesystem.go`)

Provides 7 file-operation tools backed by `wickfs.FileSystem` (LocalFS or RemoteFS).

| Phase | Active | Operation |
|-------|--------|-----------|
| BeforeAgent | Yes | Registers 7 tools: `ls`, `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `execute` |
| ModifyRequest | No | No-op |
| WrapModelCall | No | Pass-through |
| AfterModel | No | No-op (via BaseHook) |
| WrapToolCall | Yes | **Large result eviction** — if a non-file tool result exceeds 80,000 chars, truncates to first + last 2,000 chars |

**Declared active phases:** `before_agent`, `wrap_tool_call`

### 2. SkillsHook (`hooks/skills.go`)

Discovers skill definitions (SKILL.md files) and exposes them to the LLM via system prompt injection.

| Phase | Active | Operation |
|-------|--------|-----------|
| BeforeAgent | Yes | Scans configured paths for `SKILL.md` files, parses YAML frontmatter (`name`, `description`), builds in-memory `[]SkillEntry` catalog. Deduplicates by path. |
| ModifyRequest | Yes | **Appends skills catalog to system prompt** — lists each enabled skill as `- [name] description → Read path for full instructions`. Filters out user-disabled skills. |
| WrapModelCall | No | Pass-through |
| AfterModel | No | No-op (via BaseHook) |
| WrapToolCall | No | Pass-through |

**Declared active phases:** `before_agent`, `modify_request`

### 3. LazySkillsHook (`hooks/lazy_skills.go`)

Replaces `SkillsHook` as the **default** skill hook. Instead of injecting all skill prompts eagerly, it registers 3 meta-tools and only injects the active skill's prompt on demand.

| Phase | Active | Operation |
|-------|--------|-----------|
| BeforeAgent | Yes | Scans for SKILL.md files (same as SkillsHook). Registers 3 meta-tools on state: `list_skills` (returns skill catalog), `activate_skill` (loads a skill's prompt into context), `deactivate_skill` (removes the active skill's prompt). |
| ModifyRequest | Yes | **Appends active skill prompt** — if `state.ActiveSkill` is set, injects only that skill's full SKILL.md content. Also appends: "Call list_skills to discover skills. Call activate_skill to load one." |
| WrapModelCall | No | Pass-through |
| AfterModel | No | No-op (via BaseHook) |
| WrapToolCall | No | Pass-through |

**Declared active phases:** `before_agent`, `modify_request`

Note: `SkillsHook` still exists for backward compatibility but `LazySkillsHook` is now the default in `buildAgent()`.

### 4. MemoryHook (`hooks/memory.go`)

Loads persistent memory files (AGENTS.md) and injects them into the system prompt.

| Phase | Active | Operation |
|-------|--------|-----------|
| BeforeAgent | Yes | Reads AGENTS.md files from configured paths via `cat`. Concatenates content with `---` separators. |
| ModifyRequest | Yes | **Appends memory to system prompt** — wraps content in `<agent_memory>` tags with usage guidelines (persist across conversations, update via edit_file, keep concise). |
| WrapModelCall | No | Pass-through |
| AfterModel | No | No-op (via BaseHook) |
| WrapToolCall | No | Pass-through |

**Declared active phases:** `before_agent`, `modify_request`

### 5. TodoListHook (`hooks/todolist.go`)

Tracks task progress via `write_todos` and `update_todo` tools. The most active hook — uses 3 phases.

| Phase | Active | Operation |
|-------|--------|-----------|
| BeforeAgent | Yes | Initializes `state.Todos = []`. Registers 2 tools: `write_todos` (replaces entire list) and `update_todo` (single-task status change by ID). |
| ModifyRequest | Yes | **Appends todo guidance + current progress to system prompt** — injects usage instructions (when to use, task states, management rules) and a `## Current Task Progress` section listing all todos with their status. |
| WrapModelCall | No | Pass-through |
| AfterModel | Yes | **Rejects conflicting parallel tool calls** before dispatch: 2+ `write_todos` → reject all; 2+ `update_todo` → reject all; `write_todos` + `update_todo` in same turn → reject `update_todo`. Returns pre-built error results for rejected calls. |
| WrapToolCall | No | No-op (via BaseHook) |

**Declared active phases:** `before_agent`, `modify_request`, `after_model`

### 6. PhasedHook (`hooks/phased.go`)

Implements Plan→Execute→Verify phased tool gating. Controls which tools the LLM sees per phase via `state.ToolFilter`.

| Phase | Active | Operation |
|-------|--------|-----------|
| BeforeAgent | Yes | Initializes `state.Phase = PhasePlan`. Builds a tool catalog mapping tool names to categories. |
| ModifyRequest | Yes | **Sets ToolFilter based on current phase.** Plan phase: only todo + skill meta-tools visible. Execute phase: tools based on `todo.ToolHint` + category matching. Verify phase: only verification tools. Auto-transitions phase based on todo state. |
| WrapModelCall | No | Pass-through |
| AfterModel | Yes | **Rejects tool calls outside current phase.** If the LLM calls a tool not in the current phase's ToolFilter, returns a pre-built error result explaining the tool is not available in this phase. |
| WrapToolCall | No | Pass-through |

**Declared active phases:** `before_agent`, `modify_request`, `after_model`

**Phase transitions:**
- `PhasePlan` → `PhaseExecute`: when todos exist and at least one is `in_progress`
- `PhaseExecute` → `PhaseVerify`: when all todos are `done`
- `PhaseVerify` → `PhaseExecute`: if any todo is re-opened

### 7. SummarizationHook (`hooks/summarization.go`)

Compresses conversation context when it approaches the model's context window limit.

| Phase | Active | Operation |
|-------|--------|-----------|
| BeforeAgent | No | No-op (via BaseHook) |
| ModifyRequest | No | No-op |
| WrapModelCall | Yes | **Context compression** — estimates tokens (len/4 heuristic). If >85% of context window: splits messages into old (90%) + recent (10%), calls LLM to summarize old messages into ~2000 words, replaces old messages with a `[Conversation Summary]` system message, then calls `next()` with compressed messages. Falls back to pass-through on summarization failure. |
| AfterModel | No | No-op (via BaseHook) |
| WrapToolCall | No | Pass-through |

**Declared active phases:** `wrap_model_call`

## System Prompt Modification

Only the **ModifyRequest** phase can modify the system prompt. Three hooks actively modify it:

```
Base System Prompt
  ↓
+ LazySkillsHook.ModifyRequest → appends active skill prompt (if any) + "Call list_skills to discover skills."
  ↓
+ PhasedHook.ModifyRequest     → sets state.ToolFilter based on current phase (plan/execute/verify)
  ↓
+ MemoryHook.ModifyRequest     → appends <agent_memory> block with AGENTS.md content
  ↓
+ TodoListHook.ModifyRequest   → appends todo usage guidance + current task progress
  ↓
Final System Prompt (sent to LLM)
```

Note: `SkillsHook` (eager) still exists but is no longer the default. `LazySkillsHook` replaces it in `buildAgent()`.

Note: `SummarizationHook` modifies **messages** (not the system prompt) in `WrapModelCall` by replacing old messages with a summary.

Note: `PhasedHook.ModifyRequest` sets `state.ToolFilter` which the agent loop applies *after* ModifyRequest to remove tools from `toolMap` before building schemas. This means the ToolFilter affects which tool schemas the LLM sees, not the system prompt text.

## Phase × Hook Matrix

| Phase | Filesystem | Skills (eager) | LazySkills (default) | Memory | TodoList | Phased | Summarization |
|-------|:----------:|:--------------:|:--------------------:|:------:|:--------:|:------:|:-------------:|
| **BeforeAgent** | Register 7 tools | Discover SKILL.md | Discover SKILL.md, register 3 meta-tools | Load AGENTS.md | Init todos, register 2 tools | Init phase, build catalog | — |
| **ModifyRequest** | — | Inject skills catalog | Inject active skill prompt | Inject `<agent_memory>` | Inject todo prompt + progress | Set ToolFilter per phase | — |
| **WrapModelCall** | — | — | — | — | — | — | Context compression |
| **AfterModel** | — | — | — | — | Reject conflicting todo calls | Reject out-of-phase tools | — |
| **WrapToolCall** | Large result eviction | — | — | — | — | — | — |

`—` = no-op or pass-through (delegates to `BaseHook` defaults)

## BaseHook

All hooks embed `agent.BaseHook` which provides no-op defaults for every phase:

- `BeforeAgent` → returns nil
- `ModifyRequest` → returns inputs unchanged
- `WrapModelCall` → calls `next()` (pass-through)
- `AfterModel` → returns nil (no interceptions)
- `WrapToolCall` → calls `next()` (pass-through)

This allows hooks to only override the phases they care about.
