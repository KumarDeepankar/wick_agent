package agent

import (
	"fmt"
	"sync"
)

// Registry manages agent templates (from agents.yaml) and per-user scoped instances.
type Registry struct {
	mu        sync.RWMutex
	templates map[string]*Template    // key = agent_id
	instances map[string]*Instance    // key = "agent_id:username"
}

// Template is an agent configuration loaded from agents.yaml (not yet instantiated).
type Template struct {
	AgentID string
	Config  *AgentConfig
}

// HookOverrides allows runtime customization of the hook chain.
type HookOverrides struct {
	Remove []string       `json:"remove"` // hook names to skip
	Add    []string       `json:"add"`    // hook names to add
	Config map[string]any `json:"config"` // per-hook config (e.g. memory paths)
}

// Instance is a user-scoped live agent created from a template.
type Instance struct {
	AgentID       string
	Username      string
	Config        *AgentConfig
	Agent         *Agent // set after first use (lazy clone)
	HookOverrides *HookOverrides
	// Backend and other runtime state
	BackendID string
}

// NewRegistry creates an empty agent registry.
func NewRegistry() *Registry {
	return &Registry{
		templates: make(map[string]*Template),
		instances: make(map[string]*Instance),
	}
}

func scopedKey(agentID, username string) string {
	return agentID + ":" + username
}

// RegisterTemplate stores an agent template from config.
func (r *Registry) RegisterTemplate(agentID string, cfg *AgentConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.templates[agentID] = &Template{
		AgentID: agentID,
		Config:  cfg,
	}
}

// ListTemplates returns all template IDs.
func (r *Registry) ListTemplates() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.templates))
	for id := range r.templates {
		ids = append(ids, id)
	}
	return ids
}

// AllConfigs returns all template configs (for scanning skills, etc.).
func (r *Registry) AllConfigs() []*AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfgs := make([]*AgentConfig, 0, len(r.templates))
	for _, tmpl := range r.templates {
		cfgs = append(cfgs, tmpl.Config)
	}
	return cfgs
}

// TemplateCount returns the number of registered templates.
func (r *Registry) TemplateCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.templates)
}

// GetOrClone returns the user's agent instance, cloning from template if needed.
func (r *Registry) GetOrClone(agentID, username string) (*Instance, error) {
	key := scopedKey(agentID, username)

	r.mu.RLock()
	inst, ok := r.instances[key]
	r.mu.RUnlock()
	if ok {
		return inst, nil
	}

	// Clone from template
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if inst, ok := r.instances[key]; ok {
		return inst, nil
	}

	tmpl, ok := r.templates[agentID]
	if !ok {
		return nil, fmt.Errorf("agent template %q not found", agentID)
	}

	// Deep copy config
	cfgCopy := *tmpl.Config
	inst = &Instance{
		AgentID:  agentID,
		Username: username,
		Config:   &cfgCopy,
	}
	r.instances[key] = inst
	return inst, nil
}

// GetInstance returns an existing instance or nil.
func (r *Registry) GetInstance(agentID, username string) *Instance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.instances[scopedKey(agentID, username)]
}

// DeleteInstance removes a user's agent instance.
func (r *Registry) DeleteInstance(agentID, username string) error {
	key := scopedKey(agentID, username)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.instances[key]; !ok {
		return fmt.Errorf("agent %q not found for user %q", agentID, username)
	}
	delete(r.instances, key)
	return nil
}

// ListAgents returns agent info for a specific user.
// Includes live instances + un-cloned templates as placeholders.
func (r *Registry) ListAgents(username string) []AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []AgentInfo
	seen := make(map[string]bool)

	// Live instances for this user
	for _, inst := range r.instances {
		if inst.Username == username {
			result = append(result, instanceToInfo(inst))
			seen[inst.AgentID] = true
		}
	}

	// Templates not yet cloned
	for id, tmpl := range r.templates {
		if !seen[id] {
			result = append(result, templateToInfo(tmpl))
		}
	}

	return result
}

// UpdateInstanceConfig updates configuration for an existing instance.
// UpdateHookOverrides atomically updates the hook overrides for an instance
// and forces an agent rebuild on the next use.
func (r *Registry) UpdateHookOverrides(agentID, username string, overrides *HookOverrides) error {
	key := scopedKey(agentID, username)

	r.mu.Lock()
	defer r.mu.Unlock()

	inst, ok := r.instances[key]
	if !ok {
		return fmt.Errorf("agent %q not found for user %q", agentID, username)
	}
	inst.HookOverrides = overrides
	inst.Agent = nil // force rebuild on next use
	return nil
}

// InvalidateAllAgents forces all cached agent instances to rebuild on next use.
// Called when external tools are registered/deregistered so agents pick up the changes.
func (r *Registry) InvalidateAllAgents() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, inst := range r.instances {
		inst.Agent = nil
	}
}

func (r *Registry) UpdateInstanceConfig(agentID, username string, cfg *AgentConfig) error {
	key := scopedKey(agentID, username)

	r.mu.Lock()
	defer r.mu.Unlock()

	inst, ok := r.instances[key]
	if !ok {
		return fmt.Errorf("agent %q not found for user %q", agentID, username)
	}
	inst.Config = cfg
	inst.Agent = nil // force rebuild on next use
	return nil
}

func instanceToInfo(inst *Instance) AgentInfo {
	cfg := inst.Config
	info := AgentInfo{
		AgentID:      inst.AgentID,
		Tools:        nonNilStrings(cfg.Tools),
		Subagents:    subagentNames(cfg.Subagents),
		Middleware:   nonNilStrings(cfg.Middleware),
		Hooks:        []string{},
		BackendType:  "state",
		Skills:       []string{},
		LoadedSkills: []string{},
		Memory:       []string{},
		Model:        cfg.ModelStr(),
		Debug:        cfg.Debug,
	}
	// Populate hook names from the live Agent if available
	if inst.Agent != nil {
		for _, h := range inst.Agent.Hooks {
			info.Hooks = append(info.Hooks, h.Name())
		}
	} else {
		info.Hooks = defaultHookNames(cfg)
	}
	if cfg.Name != "" {
		info.Name = &cfg.Name
	}
	if cfg.SystemPrompt != "" {
		sp := truncate(cfg.SystemPrompt, 120)
		info.SystemPrompt = &sp
	}
	if cfg.Backend != nil {
		info.BackendType = cfg.Backend.Type
		if cfg.Backend.DockerHost != "" {
			info.SandboxURL = &cfg.Backend.DockerHost
		}
	}
	if cfg.Skills != nil {
		info.Skills = cfg.Skills.Paths
	}
	if cfg.Memory != nil {
		info.Memory = cfg.Memory.Paths
	}
	return info
}

func templateToInfo(tmpl *Template) AgentInfo {
	cfg := tmpl.Config
	info := AgentInfo{
		AgentID:      tmpl.AgentID,
		Tools:        nonNilStrings(cfg.Tools),
		Subagents:    subagentNames(cfg.Subagents),
		Middleware:   nonNilStrings(cfg.Middleware),
		Hooks:        defaultHookNames(cfg),
		BackendType:  "state",
		Skills:       []string{},
		LoadedSkills: []string{},
		Memory:       []string{},
		Model:        cfg.ModelStr(),
		Debug:        cfg.Debug,
	}
	if cfg.Name != "" {
		info.Name = &cfg.Name
	}
	if cfg.SystemPrompt != "" {
		sp := truncate(cfg.SystemPrompt, 120)
		info.SystemPrompt = &sp
	}
	if cfg.Backend != nil {
		info.BackendType = cfg.Backend.Type
		if cfg.Backend.DockerHost != "" {
			info.SandboxURL = &cfg.Backend.DockerHost
		}
	}
	return info
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func subagentNames(subs []SubAgentCfg) []string {
	names := make([]string, len(subs))
	for i, sa := range subs {
		names[i] = sa.Name
	}
	return names
}

// defaultHookNames returns the hook names that buildAgent would create
// for a given config, so the UI can show correct toggles before first use.
func defaultHookNames(cfg *AgentConfig) []string {
	names := []string{"tracing", "todolist"}
	// filesystem is added when a backend is configured
	if cfg.Backend != nil {
		names = append(names, "filesystem")
	}
	if cfg.Skills != nil && len(cfg.Skills.Paths) > 0 && cfg.Backend != nil {
		names = append(names, "skills")
	}
	if cfg.Memory != nil && len(cfg.Memory.Paths) > 0 && cfg.Backend != nil {
		names = append(names, "memory")
	}
	names = append(names, "summarization")
	return names
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
