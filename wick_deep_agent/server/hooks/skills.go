package hooks

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	paths   []string // container-side paths
	prefs   *agent.SkillPrefs
	skills  []SkillEntry
}

// NewSkillsHook creates a skills hook that scans the given paths.
// prefs may be nil (all skills enabled by default).
func NewSkillsHook(b backend.Backend, paths []string, prefs *agent.SkillPrefs) *SkillsHook {
	return &SkillsHook{
		backend: b,
		paths:   paths,
		prefs:   prefs,
	}
}

// Skills returns the discovered skill entries (for API listing).
func (h *SkillsHook) Skills() []SkillEntry {
	return h.skills
}

func (h *SkillsHook) Name() string { return "skills" }

func (h *SkillsHook) Phases() []string {
	return []string{"before_agent", "modify_request"}
}

// BeforeAgent scans skills directories and parses YAML frontmatter.
// Re-scans on each run to pick up newly added skills; deduplicates by path.
// Note: Host→container sync is handled by SyncHostSkills at container launch time,
// not here — skills are already present when the first message arrives.
func (h *SkillsHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	// Combine config paths with user-added extra paths
	allPaths := make([]string, 0, len(h.paths))
	allPaths = append(allPaths, h.paths...)
	if h.prefs != nil {
		allPaths = append(allPaths, h.prefs.ExtraPaths...)
	}

	// Track already-known paths to avoid duplicates across repeated BeforeAgent calls
	seen := make(map[string]bool, len(h.skills))
	for _, s := range h.skills {
		seen[s.Path] = true
	}

	for _, skillsDir := range allPaths {
		// List skill directories
		result := h.backend.Execute(fmt.Sprintf("find %s -name SKILL.md -type f 2>/dev/null", shellQuote(skillsDir)))
		if !strings.Contains(result.Output, "SKILL.md") {
			log.Printf("[skills] scan %s: no SKILL.md found (exit=%d, output=%q)", skillsDir, result.ExitCode, strings.TrimSpace(result.Output))
			continue
		}

		for _, mdPath := range strings.Split(strings.TrimSpace(result.Output), "\n") {
			mdPath = strings.TrimSpace(mdPath)
			if mdPath == "" || seen[mdPath] {
				continue
			}
			seen[mdPath] = true

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

			log.Printf("[skills] discovered: %s (%s)", entry.Name, mdPath)
			h.skills = append(h.skills, entry)
		}
	}

	log.Printf("[skills] total skills: %d (paths=%v)", len(h.skills), allPaths)
	return nil
}

// ModifyRequest injects the skills catalog into the system prompt string.
func (h *SkillsHook) ModifyRequest(ctx context.Context, systemPrompt string, msgs []agent.Message) (string, []agent.Message, error) {
	if len(h.skills) == 0 {
		return systemPrompt, msgs, nil
	}

	// Build catalog text, filtering out disabled skills
	var sb strings.Builder
	sb.WriteString("\n\nAvailable Skills:\n")
	count := 0
	for _, skill := range h.skills {
		if h.prefs != nil && h.prefs.Disabled[skill.Name] {
			continue
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s → Read %s for full instructions\n",
			skill.Name, skill.Description, skill.Path))
		count++
	}

	if count == 0 {
		return systemPrompt, msgs, nil
	}

	return systemPrompt + sb.String(), msgs, nil
}

func (h *SkillsHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	return next(ctx, msgs)
}

func (h *SkillsHook) WrapToolCall(ctx context.Context, call agent.ToolCall, next agent.ToolCallFunc) (*agent.ToolResult, error) {
	return next(ctx, call)
}

// ── Standalone sync (called at container launch, not per-message) ────────────

// SyncHostSkills copies skill files from host paths into the container.
// Called from DockerBackend.OnReady at container launch time so skills are
// available immediately, not deferred to the first user message.
func SyncHostSkills(b backend.Backend, containerPaths, hostPaths []string) {
	for i, hostPath := range hostPaths {
		if i >= len(containerPaths) {
			break
		}
		containerPath := containerPaths[i]

		// Check if host path exists
		info, err := os.Stat(hostPath)
		if err != nil || !info.IsDir() {
			continue
		}

		// Check if container path already has skills.
		// Use strings.Contains("SKILL.md") instead of checking for non-empty output,
		// because buildExecResponse replaces empty stdout with "<no output>" sentinel
		// which fools empty-string checks. Also, "| head -1" masks find's exit code.
		result := b.Execute(fmt.Sprintf("find %s -name SKILL.md -type f 2>/dev/null | head -1", shellQuote(containerPath)))
		if strings.Contains(result.Output, "SKILL.md") {
			log.Printf("[skills] container path %s already has skills, skipping sync", containerPath)
			continue
		}

		// Walk host directory and copy files
		count := 0
		filepath.Walk(hostPath, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			relPath, err := filepath.Rel(hostPath, path)
			if err != nil {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			targetPath := containerPath + "/" + relPath
			targetDir := containerPath + "/" + filepath.Dir(relPath)

			b.Execute(fmt.Sprintf("mkdir -p %s", shellQuote(targetDir)))

			encoded := base64.StdEncoding.EncodeToString(data)
			b.Execute(fmt.Sprintf("echo %s | base64 -d > %s", shellQuote(encoded), shellQuote(targetPath)))
			count++
			return nil
		})

		if count > 0 {
			log.Printf("[skills] synced %d files from host %s → container %s", count, hostPath, containerPath)
		}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
