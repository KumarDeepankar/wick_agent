package main

import (
	"log"

	wickserver "wick_server"
)

func main() {
	cfg := wickserver.LoadAppConfig()

	opts := []wickserver.Option{
		wickserver.WithHost(cfg.Host),
		wickserver.WithPort(cfg.Port),
	}
	if cfg.WickGatewayURL != "" {
		opts = append(opts, wickserver.WithGateway(cfg.WickGatewayURL))
	}
	if cfg.ConfigFile != "" {
		opts = append(opts, wickserver.WithConfigFile(cfg.ConfigFile))
	}

	s := wickserver.New(opts...)
	if err := s.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
