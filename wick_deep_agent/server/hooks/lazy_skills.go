package hooks

import (
	"context"
	"fmt"
	"log"
	"strings"

	"wick_server/agent"
	"wick_server/backend"
	"wick_server/llm"

	"gopkg.in/yaml.v3"
)

// LazySkillsHook discovers skills but only loads one at a time.
// Registers list_skills and activate_skill meta-tools instead of injecting
// all skill prompts upfront. Keeps the base system prompt small.
type LazySkillsHook struct {
	agent.BaseHook
	backend      backend.Backend
	paths        []string
	prefs        *agent.SkillPrefs
	skills       []SkillEntry
	autoActivate string // if set, auto-activate this skill after discovery
}

// NewLazySkillsHook creates a lazy skills hook that scans the given paths.
func NewLazySkillsHook(b backend.Backend, paths []string, prefs *agent.SkillPrefs) *LazySkillsHook {
	return &LazySkillsHook{
		backend: b,
		paths:   paths,
		prefs:   prefs,
	}
}

// Skills returns the discovered skill entries (for API listing).
func (h *LazySkillsHook) Skills() []SkillEntry {
	return h.skills
}

func (h *LazySkillsHook) Name() string { return "lazy_skills" }

func (h *LazySkillsHook) Phases() []string {
	return []string{"before_agent", "modify_request"}
}

// BeforeAgent scans for skills and registers the meta-tools.
func (h *LazySkillsHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	h.discoverSkills()

	// Register list_skills — returns skill names + one-line descriptions
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "list_skills",
		ToolDesc: "List all available skills with their names and descriptions. Call this to discover what specialized capabilities are available before activating one.",
		ToolParams: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			return h.listSkills(state), nil
		},
	})

	// Register activate_skill — loads a skill's full prompt
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "activate_skill",
		ToolDesc: "Activate a skill by name to load its full instructions and tools. Only one skill can be active at a time — activating a new skill deactivates the previous one.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Name of the skill to activate",
				},
			},
			"required": []string{"name"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			name, _ := args["name"].(string)
			if name == "" {
				return "Error: 'name' is required", nil
			}
			return h.activateSkill(state, name), nil
		},
	})

	// Register deactivate_skill — unloads the active skill
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "deactivate_skill",
		ToolDesc: "Deactivate the currently active skill, removing its instructions and tools.",
		ToolParams: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			return h.deactivateSkill(state), nil
		},
	})

	// Auto-activate a matching skill if configured (used by sub-agents)
	if h.autoActivate != "" {
		h.activateSkill(state, h.autoActivate)
	}

	return nil
}

// ModifyRequest injects the skill catalog and the active skill's prompt (if any).
func (h *LazySkillsHook) ModifyRequest(ctx context.Context, systemPrompt string, msgs []agent.Message) (string, []agent.Message, error) {
	state := agent.StateFromContext(ctx)

	// Inject skill names so the LLM knows what's available (descriptions load on activate)
	if len(h.skills) > 0 {
		var names []string
		for _, skill := range h.skills {
			if h.prefs != nil && h.prefs.Disabled[skill.Name] {
				continue
			}
			names = append(names, skill.Name)
		}
		if len(names) > 0 {
			systemPrompt += "\n\nAvailable skills: " + strings.Join(names, ", ") + ". Call activate_skill by name to load instructions."
		}
	}

	// Inject the active skill's full prompt
	if state != nil && state.ActiveSkillPrompt != "" {
		systemPrompt += "\n\n## Active Skill\n" + state.ActiveSkillPrompt
	}

	return systemPrompt, msgs, nil
}

func (h *LazySkillsHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	return next(ctx, msgs)
}

// discoverSkills scans configured paths for SKILL.md files.
func (h *LazySkillsHook) discoverSkills() {
	allPaths := make([]string, 0, len(h.paths))
	allPaths = append(allPaths, h.paths...)
	if h.prefs != nil {
		allPaths = append(allPaths, h.prefs.ExtraPaths...)
	}

	seen := make(map[string]bool, len(h.skills))
	for _, s := range h.skills {
		seen[s.Path] = true
	}

	for _, skillsDir := range allPaths {
		result := h.backend.Execute(fmt.Sprintf("find %s -name SKILL.md -type f 2>/dev/null", shellQuote(skillsDir)))
		if !strings.Contains(result.Output, "SKILL.md") {
			continue
		}

		for _, mdPath := range strings.Split(strings.TrimSpace(result.Output), "\n") {
			mdPath = strings.TrimSpace(mdPath)
			if mdPath == "" || seen[mdPath] {
				continue
			}
			seen[mdPath] = true

			readResult := h.backend.Execute(fmt.Sprintf("cat %s", shellQuote(mdPath)))
			if readResult.ExitCode != 0 {
				continue
			}

			entry := SkillEntry{Path: mdPath}
			parts := strings.Split(mdPath, "/")
			if len(parts) >= 2 {
				entry.Name = parts[len(parts)-2]
			}

			match := frontmatterRE.FindStringSubmatch(readResult.Output)
			if match != nil {
				var front map[string]any
				if err := yaml.Unmarshal([]byte(match[1]), &front); err == nil {
					if name, ok := front["name"].(string); ok {
						entry.Name = name
					}
					if desc, ok := front["description"].(string); ok {
						entry.Description = strings.TrimSpace(desc)
					}
				}
			}

			log.Printf("[lazy_skills] discovered: %s (%s)", entry.Name, mdPath)
			h.skills = append(h.skills, entry)
		}
	}
}

// listSkills returns a compact list of available skill names.
func (h *LazySkillsHook) listSkills(state *agent.AgentState) string {
	if len(h.skills) == 0 {
		return "No skills available."
	}

	var names []string
	for _, skill := range h.skills {
		if h.prefs != nil && h.prefs.Disabled[skill.Name] {
			continue
		}
		name := skill.Name
		if state.ActiveSkill == skill.Name {
			name += " (ACTIVE)"
		}
		names = append(names, name)
	}
	return "Available skills: " + strings.Join(names, ", ") + ". Call activate_skill to load one."
}

// WithAutoActivate sets a skill name to auto-activate after discovery in BeforeAgent.
// If the name matches a discovered skill, it is activated before the first LLM call.
// If no match, this is a no-op. Chainable.
func (h *LazySkillsHook) WithAutoActivate(name string) *LazySkillsHook {
	h.autoActivate = name
	return h
}

// activateSkill loads a skill's full prompt and deactivates the previous one.
func (h *LazySkillsHook) activateSkill(state *agent.AgentState, name string) string {
	// Find the skill
	var found *SkillEntry
	for i := range h.skills {
		if h.skills[i].Name == name {
			found = &h.skills[i]
			break
		}
	}
	if found == nil {
		return fmt.Sprintf("Error: skill %q not found. Call list_skills to see available skills.", name)
	}

	if h.prefs != nil && h.prefs.Disabled[name] {
		return fmt.Sprintf("Error: skill %q is disabled.", name)
	}

	// Deactivate previous skill if any
	if state.ActiveSkill != "" && state.ActiveSkill != name {
		h.deactivateSkill(state)
	}

	// Read the skill's full content
	readResult := h.backend.Execute(fmt.Sprintf("cat %s", shellQuote(found.Path)))
	if readResult.ExitCode != 0 {
		return fmt.Sprintf("Error: could not read skill file at %s", found.Path)
	}

	// Strip frontmatter, keep the body as the active prompt
	body := readResult.Output
	if match := frontmatterRE.FindStringIndex(body); match != nil {
		body = body[match[1]:]
	}

	state.ActiveSkillPrompt = strings.TrimSpace(body)
	state.ActiveSkill = name

	log.Printf("[lazy_skills] activated skill: %s", name)
	return fmt.Sprintf("Skill %q activated. Its instructions are now loaded.", name)
}

// deactivateSkill unloads the active skill.
func (h *LazySkillsHook) deactivateSkill(state *agent.AgentState) string {
	if state.ActiveSkill == "" {
		return "No skill is currently active."
	}

	prev := state.ActiveSkill
	state.ActiveSkillPrompt = ""
	state.ActiveSkill = ""

	log.Printf("[lazy_skills] deactivated skill: %s", prev)
	return fmt.Sprintf("Skill %q deactivated.", prev)
}

// SyncHostSkillsLazy copies skill files from host to container. Reuses SyncHostSkills.
func SyncHostSkillsLazy(b backend.Backend, containerPaths, hostPaths []string) {
	SyncHostSkills(b, containerPaths, hostPaths)
}
