package main

import (
	"os"

	"gopkg.in/yaml.v3"
)

type DownstreamServer struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

type Config struct {
	Listen     string             `yaml:"listen"`
	Downstream []DownstreamServer `yaml:"downstream"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	return &cfg, nil
}
