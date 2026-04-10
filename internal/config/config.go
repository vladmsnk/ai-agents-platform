package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Provider struct {
	Name    string   `yaml:"name" json:"name"`
	URL     string   `yaml:"url" json:"url"`
	Models  []string `yaml:"models" json:"models"`
	Weight  int      `yaml:"weight" json:"weight"`
	Enabled bool     `yaml:"enabled" json:"enabled"`
	APIKey  string   `yaml:"-" json:"-"`
	KeyEnv  string   `yaml:"key_env" json:"key_env,omitempty"`
	Timeout int      `yaml:"timeout_seconds" json:"timeout_seconds"`
}

type Config struct {
	Listen      string     `yaml:"listen" json:"listen"`
	DatabaseURL string     `yaml:"database_url" json:"-"`
	Providers   []Provider `yaml:"providers" json:"providers"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}

	// Allow DATABASE_URL env to override config file
	if env := os.Getenv("DATABASE_URL"); env != "" {
		cfg.DatabaseURL = env
	}

	for i := range cfg.Providers {
		ApplyDefaults(&cfg.Providers[i])
	}

	return &cfg, nil
}

func ProviderURLs(providers []Provider) map[string]string {
	urls := make(map[string]string, len(providers))
	for _, p := range providers {
		urls[p.Name] = p.URL
	}
	return urls
}

func ApplyDefaults(p *Provider) {
	if p.Weight <= 0 {
		p.Weight = 1
	}
	if p.Timeout <= 0 {
		p.Timeout = 60
	}
	if p.KeyEnv != "" && p.APIKey == "" {
		p.APIKey = os.Getenv(p.KeyEnv)
	}
}

func ValidateProvider(p Provider) error {
	if p.Name == "" {
		return fmt.Errorf("name is required")
	}
	if p.URL == "" {
		return fmt.Errorf("url is required")
	}
	if len(p.Models) == 0 {
		return fmt.Errorf("at least one model is required")
	}
	return nil
}
