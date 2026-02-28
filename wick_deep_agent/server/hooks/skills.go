package hooks

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"wick_server/agent"
	"wick_server/backend"
	"wick_server/llm"

	"gopkg.in/yaml.v3"
)

var frontmatterRE = regexp.MustCompile(`(?s)\A---\s*\n(.*?\n)---\s*\n`)

// SkillEntry represents a discovered skill.
type SkillEntry struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Path        string // directory path
}

// SkillsHook discovers SKILL.md files and injects a catalog into the system prompt.
// Progressive loading: only metadata in prompt, agent calls read_file on-demand.
type SkillsHook struct {
	agent.BaseHook
	backend backend.Backend
	paths   []string
	skills  []SkillEntry
}

// NewSkillsHook creates a skills hook that scans the given paths.
func NewSkillsHook(b backend.Backend, paths []string) *SkillsHook {
	return &SkillsHook{
		backend: b,
		paths:   paths,
	}
}

func (h *SkillsHook) Name() string { return "skills" }

func (h *SkillsHook) Phases() []string {
	return []string{"before_agent", "modify_request"}
}

// BeforeAgent scans skills directories and parses YAML frontmatter.
func (h *SkillsHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	for _, skillsDir := range h.paths {
		// List skill directories
		result := h.backend.Execute(fmt.Sprintf("find %s -name SKILL.md -type f 2>/dev/null", shellQuote(skillsDir)))
		if result.ExitCode != 0 || strings.TrimSpace(result.Output) == "" {
			continue
		}

		for _, mdPath := range strings.Split(strings.TrimSpace(result.Output), "\n") {
			mdPath = strings.TrimSpace(mdPath)
			if mdPath == "" {
				continue
			}

			// Read the SKILL.md file
			readResult := h.backend.Execute(fmt.Sprintf("cat %s", shellQuote(mdPath)))
			if readResult.ExitCode != 0 {
				continue
			}

			entry := SkillEntry{Path: mdPath}

			// Extract directory name as fallback name
			parts := strings.Split(mdPath, "/")
			if len(parts) >= 2 {
				entry.Name = parts[len(parts)-2]
			}

			// Parse YAML frontmatter
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

			h.skills = append(h.skills, entry)
		}
	}
	return nil
}

// ModifyRequest injects the skills catalog into the system prompt.
func (h *SkillsHook) ModifyRequest(ctx context.Context, msgs []agent.Message) ([]agent.Message, error) {
	if len(h.skills) == 0 {
		return msgs, nil
	}

	// Build catalog text
	var sb strings.Builder
	sb.WriteString("\n\nAvailable Skills:\n")
	for _, skill := range h.skills {
		sb.WriteString(fmt.Sprintf("- [%s] %s → Read %s for full instructions\n",
			skill.Name, skill.Description, skill.Path))
	}

	// Find or create system message and append catalog
	if len(msgs) > 0 && msgs[0].Role == "system" {
		msgs[0].Content += sb.String()
	} else {
		// Prepend a system message with the catalog
		sysMsg := agent.Message{
			Role:    "system",
			Content: sb.String(),
		}
		msgs = append([]agent.Message{sysMsg}, msgs...)
	}

	return msgs, nil
}

func (h *SkillsHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	return next(ctx, msgs)
}

func (h *SkillsHook) WrapToolCall(ctx context.Context, call agent.ToolCall, next agent.ToolCallFunc) (*agent.ToolResult, error) {
	return next(ctx, call)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
