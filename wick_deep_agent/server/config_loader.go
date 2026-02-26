package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"wick_go/agent"
	"wick_go/backend"
	"wick_go/handlers"
)

// configFile is the top-level structure of agents.yaml.
type configFile struct {
	Defaults *configDefaults               `yaml:"defaults"`
	Agents   map[string]*agent.AgentConfig `yaml:"agents"`
}

type configDefaults struct {
	Backend *agent.BackendCfg `yaml:"backend"`
	Debug   bool              `yaml:"debug"`
}

// loadConfigFile reads agents.yaml, registers agents and launches backends.
func loadConfigFile(path string, deps *handlers.Deps) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	configDir, _ := filepath.Abs(filepath.Dir(path))

	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	for agentID, agentCfg := range cfg.Agents {
		// Merge defaults
		if cfg.Defaults != nil {
			if agentCfg.Backend == nil && cfg.Defaults.Backend != nil {
				agentCfg.Backend = cfg.Defaults.Backend
			}
			if !agentCfg.Debug && cfg.Defaults.Debug {
				agentCfg.Debug = true
			}
		}

		// Resolve relative paths in skills config against config file directory
		if agentCfg.Skills != nil {
			for i, p := range agentCfg.Skills.Paths {
				if !filepath.IsAbs(p) {
					agentCfg.Skills.Paths[i] = filepath.Join(configDir, p)
				}
			}
		}

		// Resolve relative paths in memory config
		if agentCfg.Memory != nil {
			for i, p := range agentCfg.Memory.Paths {
				if !filepath.IsAbs(p) {
					agentCfg.Memory.Paths[i] = filepath.Join(configDir, p)
				}
			}
		}

		// Register agent template
		deps.Registry.RegisterTemplate(agentID, agentCfg)

		// Initialize backend
		if agentCfg.Backend != nil {
			username := "local"
			var b backend.Backend

			if agentCfg.Backend.Type == "local" {
				// Local backend — runs wickfs directly on the host
				workdir := agentCfg.Backend.Workdir
				if workdir == "" {
					workdir = filepath.Join(configDir, "workspace")
				} else if !filepath.IsAbs(workdir) {
					workdir = filepath.Join(configDir, workdir)
				}
				b = backend.NewLocalBackend(workdir, agentCfg.Backend.Timeout, agentCfg.Backend.MaxOutputBytes)
			} else {
				// Docker backend (default)
				containerName := agentCfg.Backend.ContainerName
				if containerName == "" {
					containerName = fmt.Sprintf("wick-sandbox-%s", username)
				}
				db := backend.NewDockerBackend(
					containerName, agentCfg.Backend.Workdir,
					agentCfg.Backend.Timeout, agentCfg.Backend.MaxOutputBytes,
					agentCfg.Backend.DockerHost, agentCfg.Backend.Image, username,
				)
				db.LaunchContainerAsync(func(event, user string) {
					deps.EventBus.Broadcast(event + ":" + user)
				})
				b = db
			}

			deps.Backends.Set(agentID, username, b)
		}

		log.Printf("  loaded agent %q (%s)", agentID, agentCfg.Name)
	}

	return nil
}
