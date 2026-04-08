# Sub-Agent Architecture

Sub-agents are **full agents** — they get the same hook chain as parent agents. The only difference is they run on an isolated thread and cannot delegate to other sub-agents.

## Hook Chain Comparison

| Hook | Parent Agent | Sub-Agent | Notes |
|------|-------------|-----------|-------|
| TruncationHook | Yes | Yes | Inherits `maxToolOutputChars` from parent config |
| TracingHook | Yes | Yes | Independent tracing spans |
| TodoListHook | Yes | Yes | Isolated todos per thread |
| FilesystemHook | Yes | Yes | Shared backend with parent |
| LazySkillsHook | Yes | Yes | Inherits skill paths from parent; auto-activates matching skill |
| MemoryHook | Yes | Yes | Inherits memory paths from parent |
| SubAgentHook | Yes | **No** | Prevents infinite recursion — sub-agents cannot delegate further |
| SummarizationHook | Yes | Yes | Inherits `contextWindow` from parent config |

## Skill Auto-Activation

When a sub-agent's name matches a discovered skill, the skill is **auto-activated** before the first LLM call. No extra LLM turn is needed.

```
Parent delegates to "report-generator"
  → LazySkillsHook.BeforeAgent discovers skills
  → Finds skill named "report-generator"
  → WithAutoActivate("report-generator") triggers activateSkill()
  → SKILL.md content loaded into system prompt
  → First LLM call already has full instructions
```

If no matching skill exists, this is a no-op — the sub-agent runs with just its configured `SystemPrompt` from `SubAgentCfg`.

## Delegation Flow

```
┌─────────────────────────────────────────────────────────┐
│  PARENT AGENT (e.g. opensearch-researcher)              │
│                                                         │
│  SubAgentHook.BeforeAgent                               │
│    → registers delegate_to_agent tool                   │
│    → lists available sub-agents from SubAgentCfg[]      │
│                                                         │
│  LLM calls: delegate_to_agent("report-generator", task) │
│    │                                                    │
│    ▼                                                    │
│  runSubAgent()                                          │
│    1. Resolve model (inherit from parent if unset)      │
│    2. Build AgentConfig with sa.SystemPrompt            │
│    3. Build full hook chain (same as parent, no SubAgent)│
│    4. Resolve external tools via toolLookup             │
│    5. Create isolated thread ID:                        │
│       "{parentThread}:sub:{name}:{toolCallID}"          │
│    6. Run agent (streaming or synchronous)              │
│                                                         │
│  ┌───────────────────────────────────────────────────┐  │
│  │  SUB-AGENT (report-generator)                     │  │
│  │                                                   │  │
│  │  BeforeAgent:                                     │  │
│  │    TruncationHook    — pass-through               │  │
│  │    TracingHook       — pass-through               │  │
│  │    TodoListHook      — registers todo tools       │  │
│  │    FilesystemHook    — registers file tools       │  │
│  │    LazySkillsHook    — discovers skills           │  │
│  │      → auto-activates "report-generator" skill    │  │
│  │      → SKILL.md content loaded                    │  │
│  │    MemoryHook        — loads memory files         │  │
│  │    SummarizationHook — pass-through               │  │
│  │                                                   │  │
│  │  Agent Loop (up to 25 iterations):                │  │
│  │    ModifyRequest → LLM call → tool calls → loop   │  │
│  │    (identical to parent agent loop)               │  │
│  │                                                   │  │
│  │  Result: final assistant message returned         │  │
│  └───────────────────────────────────────────────────┘  │
│                                                         │
│  Sub-agent result returned as delegate_to_agent output  │
│  Parent continues its own agent loop                    │
└─────────────────────────────────────────────────────────┘
```

## Streaming

When the parent has an active SSE connection (`parentEventCh`), sub-agent events are forwarded to the parent with mapped event names:

| Sub-agent event | Parent event | Data |
|----------------|--------------|------|
| `on_chat_model_stream` | `on_subagent_stream` | `{chunk}` |
| `on_tool_start` | `on_subagent_tool_start` | `{agent, sub_run_id, input}` |
| `on_tool_end` | `on_subagent_tool_end` | `{agent, sub_run_id, output}` |
| `on_chat_model_start` | `on_subagent_model_start` | — |
| `on_chat_model_end` | `on_subagent_model_end` | — |
| `done` | `on_subagent_done` | — |
| `error` | `on_subagent_error` | `{error}` |

All forwarded events include `RunID = parentToolID` so the UI can associate sub-agent activity with the `delegate_to_agent` tool call card.

## Thread Isolation

Each sub-agent gets its own thread ID derived from the parent's:

```
parentThread:sub:{agentName}:{parentToolCallID}
```

The `parentToolCallID` suffix ensures parallel invocations of the same sub-agent (e.g. two concurrent `delegate_to_agent("report-generator", ...)` calls) each get their own thread and don't stomp on each other's state.

## Configuration

Sub-agents are defined in `SubAgentCfg`:

```yaml
subagents:
  - name: report-generator
    description: Generates visual slide-deck reports from research artifacts
    system_prompt: "You are a report generator..."
    model: ""          # empty = inherit from parent
    tools: []          # resolved via toolLookup
```

Sub-agents inherit from the parent's `AgentConfig`:
- **Model** — used when `SubAgentCfg.Model` is empty
- **Skill paths** — `cfg.Skills.Paths` passed to `LazySkillsHook`
- **Memory paths** — `cfg.Memory.Paths` passed to `MemoryHook`
- **Backend** — shared instance (same container/filesystem)
- **Context window** — `cfg.ContextWindow` for `SummarizationHook`
- **Max tool output chars** — `cfg.Backend.MaxToolOutputChars` for `TruncationHook`

## Key Files

| File | Role |
|------|------|
| `hooks/subagent.go` | `SubAgentHook`, `runSubAgent`, streaming event forwarding |
| `hooks/lazy_skills.go` | `LazySkillsHook`, `WithAutoActivate`, skill discovery |
| `agent/config.go` | `AgentConfig`, `SubAgentCfg` |
| `agent/loop.go` | Agent loop (shared by parent and sub-agents) |
| `handlers/handlers.go` | Parent hook chain composition (`buildAgent`) |
