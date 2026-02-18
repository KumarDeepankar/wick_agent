package agent

// Message represents a chat message in the conversation.
type Message struct {
	Role       string     `json:"role"`                  // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // set when Role == "tool"
	Name       string     `json:"name,omitempty"`         // tool name when Role == "tool"
}

// ToolCall represents an LLM's request to invoke a tool.
type ToolCall struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Args     map[string]any `json:"args"`
	RawArgs  string         `json:"-"` // raw JSON string from LLM
}

// ToolResult holds the output of a tool execution.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
}

// AgentState holds the full conversation state for a thread.
type AgentState struct {
	ThreadID string    `json:"thread_id"`
	Messages []Message `json:"messages"`
	Todos    []Todo    `json:"todos,omitempty"`
	Files    map[string]string `json:"files,omitempty"` // path → content (tracked writes)

	// toolRegistry holds tools registered at runtime by hooks (e.g. FilesystemHook).
	// Not serialized — rebuilt on each agent run.
	toolRegistry map[string]Tool `json:"-"`
}

// Todo represents a task tracked by the TodoList hook.
type Todo struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // "pending", "in_progress", "done"
}

// StreamEvent is sent from the agent loop to the SSE handler.
type StreamEvent struct {
	Event    string `json:"event"`              // on_chat_model_stream, on_tool_start, on_tool_end, done, error
	Name     string `json:"name,omitempty"`     // tool name or model name
	RunID    string `json:"run_id,omitempty"`
	Data     any    `json:"data,omitempty"`
	ThreadID string `json:"thread_id,omitempty"` // set on "done" event
}

// AgentConfig is the configuration for creating an agent from agents.yaml.
type AgentConfig struct {
	Name        string         `yaml:"name" json:"name"`
	Model       any            `yaml:"model" json:"model"` // string or map
	SystemPrompt string        `yaml:"system_prompt" json:"system_prompt"`
	Tools       []string       `yaml:"tools" json:"tools"`
	Middleware  []string       `yaml:"middleware" json:"middleware"`
	Subagents   []SubAgentCfg  `yaml:"subagents" json:"subagents"`
	Backend     *BackendCfg    `yaml:"backend" json:"backend"`
	Skills      *SkillsCfg     `yaml:"skills" json:"skills"`
	Memory      *MemoryCfg     `yaml:"memory" json:"memory"`
	Debug       bool           `yaml:"debug" json:"debug"`
}

// SubAgentCfg describes a subagent template.
type SubAgentCfg struct {
	Name         string   `yaml:"name" json:"name"`
	Description  string   `yaml:"description" json:"description"`
	SystemPrompt string   `yaml:"system_prompt" json:"system_prompt"`
	Tools        []string `yaml:"tools" json:"tools"`
	Model        string   `yaml:"model" json:"model"`
}

// BackendCfg holds backend configuration.
type BackendCfg struct {
	Type           string `yaml:"type" json:"type"`                       // "local", "docker", "state"
	Workdir        string `yaml:"workdir" json:"workdir"`
	Timeout        float64 `yaml:"timeout" json:"timeout"`
	MaxOutputBytes int    `yaml:"max_output_bytes" json:"max_output_bytes"`
	DockerHost     string `yaml:"docker_host" json:"docker_host"`
	Image          string `yaml:"image" json:"image"`
	ContainerName  string `yaml:"container_name" json:"container_name"`
}

// SkillsCfg holds skills configuration.
type SkillsCfg struct {
	Paths []string `yaml:"paths" json:"paths"`
}

// MemoryCfg holds memory configuration.
type MemoryCfg struct {
	Paths          []string          `yaml:"paths" json:"paths"`
	InitialContent map[string]string `yaml:"initial_content" json:"initial_content"`
}

// AgentInfo is the JSON response for agent metadata (matches Python AgentInfo).
type AgentInfo struct {
	AgentID         string   `json:"agent_id"`
	Name            *string  `json:"name"`
	Model           string   `json:"model"`
	SystemPrompt    *string  `json:"system_prompt"`
	Tools           []string `json:"tools"`
	Subagents       []string `json:"subagents"`
	Middleware      []string `json:"middleware"`
	BackendType     string   `json:"backend_type"`
	SandboxURL      *string  `json:"sandbox_url"`
	HasInterruptOn  bool     `json:"has_interrupt_on"`
	Skills          []string `json:"skills"`
	LoadedSkills    []string `json:"loaded_skills"`
	Memory          []string `json:"memory"`
	HasResponseFmt  bool     `json:"has_response_format"`
	CacheEnabled    bool     `json:"cache_enabled"`
	Debug           bool     `json:"debug"`
	ContainerStatus *string  `json:"container_status"`
	ContainerError  *string  `json:"container_error"`
}

// ModelStr extracts a display string from the Model field (string or map).
func (c *AgentConfig) ModelStr() string {
	switch v := c.Model.(type) {
	case string:
		return v
	case map[string]any:
		prov, _ := v["provider"].(string)
		model, _ := v["model"].(string)
		if prov != "" && model != "" {
			return prov + ":" + model
		}
		if model != "" {
			return model
		}
		return prov
	default:
		return ""
	}
}
