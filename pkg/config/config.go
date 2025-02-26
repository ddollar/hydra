package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Healthcheck string
	Interval    time.Duration
	Interfaces  []Interface
}

type Interface struct {
	Device string
	Type   string
	Check  string
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	for _, i := range cfg.Interfaces {
		if i.Device == "" {
			return nil, fmt.Errorf("device is required")
		}

		if i.Type == "" {
			return nil, fmt.Errorf("type is required")
		}
	}

	return &cfg, nil
}
