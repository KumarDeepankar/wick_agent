package agent

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
	Debug         bool              `yaml:"debug" json:"debug"`
	ContextWindow int               `yaml:"context_window" json:"context_window"`
	BuiltinConfig map[string]string `yaml:"builtin_config" json:"builtin_config"`
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

// AgentInfo is the JSON response for agent metadata.
type AgentInfo struct {
	AgentID         string   `json:"agent_id"`
	Name            *string  `json:"name"`
	Model           string   `json:"model"`
	SystemPrompt    *string  `json:"system_prompt"`
	Tools           []string `json:"tools"`
	Subagents       []string `json:"subagents"`
	Middleware      []string `json:"middleware"`
	Hooks           []string `json:"hooks"`
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
