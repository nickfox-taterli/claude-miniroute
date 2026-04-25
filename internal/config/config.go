package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      ServerConfig     `yaml:"server"`
	Storage     StorageConfig    `yaml:"storage"`
	Policy      PolicyConfig     `yaml:"policy"`
	ModelRoutes ModelRoutesConfig `yaml:"model_routes"`
	Endpoints   []EndpointConfig `yaml:"endpoints"`
}

type ModelRoutesConfig struct {
	Default []string     `yaml:"default"`
	Routes []ModelRoute `yaml:"routes"`
}

type ServerConfig struct {
	Listen           string `yaml:"listen"`
	AdminListen      string `yaml:"admin_listen"`
	RequestTimeoutMS int    `yaml:"request_timeout_ms"`
	ShutdownGraceMS  int    `yaml:"shutdown_grace_ms"`
	ReadTimeoutMS    int    `yaml:"read_timeout_ms"`
	IdleTimeoutMS    int    `yaml:"idle_timeout_ms"`
}

type StorageConfig struct {
	SQLitePath        string `yaml:"sqlite_path"`
	MaxParseBodyBytes int64  `yaml:"max_parse_body_bytes"`
}

type PolicyConfig struct {
	Scheduler string `yaml:"scheduler"`
	Retry     int    `yaml:"retry"`
}

type ModelRoute struct {
	From string   `yaml:"from"`
	To   []string `yaml:"to"`
}

type EndpointConfig struct {
	Name       string   `yaml:"name"`
	APIKey     string   `yaml:"api_key"`
	AllowModel []string `yaml:"allow_model"`
	Provider   string   `yaml:"provider"`
	Rank       int      `yaml:"rank"`
	AltRank    int      `yaml:"alt_rank"`
	Enabled    bool     `yaml:"enabled"`
}

var providerBaseURLs = map[string]string{
	"MiniMax":     "https://api.minimaxi.com/anthropic",
	"GLM":         "https://open.bigmodel.cn/api/anthropic",
	"siliconflow": "https://api.siliconflow.cn/",
	"openrouter":  "https://openrouter.ai/api",
}

func (ep EndpointConfig) BaseURL() string {
	if u, ok := providerBaseURLs[ep.Provider]; ok {
		return u
	}
	return ""
}

func (ep EndpointConfig) AllowsModel(model string) bool {
	for _, m := range ep.AllowModel {
		if m == model {
			return true
		}
	}
	return false
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) withDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = "0.0.0.0:8080"
	}
	if c.Server.AdminListen == "" {
		c.Server.AdminListen = "127.0.0.1:8081"
	}
	if c.Server.RequestTimeoutMS <= 0 {
		c.Server.RequestTimeoutMS = 300000
	}
	if c.Server.ShutdownGraceMS <= 0 {
		c.Server.ShutdownGraceMS = 30000
	}
	if c.Server.ReadTimeoutMS <= 0 {
		c.Server.ReadTimeoutMS = 15000
	}
	if c.Server.IdleTimeoutMS <= 0 {
		c.Server.IdleTimeoutMS = 120000
	}
	if c.Storage.SQLitePath == "" {
		c.Storage.SQLitePath = "./data/miniroute.db"
	}
	if c.Storage.MaxParseBodyBytes <= 0 {
		c.Storage.MaxParseBodyBytes = 1 << 20
	}
	if c.Policy.Scheduler == "" {
		c.Policy.Scheduler = "sequential"
	}
	if c.Policy.Retry <= 0 {
		c.Policy.Retry = 1
	}
}

func (c *Config) Validate() error {
	c.Policy.Scheduler = strings.ToLower(c.Policy.Scheduler)
	if c.Policy.Scheduler != "sequential" && c.Policy.Scheduler != "random" {
		return fmt.Errorf("policy.scheduler must be sequential or random, got %q", c.Policy.Scheduler)
	}

	if len(c.Endpoints) == 0 {
		return errors.New("at least one endpoint is required")
	}

	names := map[string]struct{}{}
	validProviders := map[string]bool{
		"MiniMax":     true,
		"GLM":         true,
		"siliconflow": true,
		"openrouter":  true,
	}
	for i := range c.Endpoints {
		ep := &c.Endpoints[i]
		if ep.Name == "" {
			return fmt.Errorf("endpoints[%d].name is required", i)
		}
		if _, dup := names[ep.Name]; dup {
			return fmt.Errorf("duplicate endpoint name: %s", ep.Name)
		}
		names[ep.Name] = struct{}{}
		if !validProviders[ep.Provider] {
			return fmt.Errorf("endpoint %s: provider must be one of MiniMax, GLM, siliconflow, openrouter, got %q", ep.Name, ep.Provider)
		}
		if ep.APIKey == "" {
			return fmt.Errorf("endpoint %s: api_key is required", ep.Name)
		}
		if len(ep.AllowModel) == 0 {
			return fmt.Errorf("endpoint %s: allow_model is required", ep.Name)
		}
		if ep.Rank < 1 {
			return fmt.Errorf("endpoint %s: rank must be >= 1", ep.Name)
		}
		if ep.AltRank != 0 && ep.AltRank < 1 {
			return fmt.Errorf("endpoint %s: alt_rank must be >= 1 when set", ep.Name)
		}
	}
	for i, r := range c.ModelRoutes.Routes {
		if r.From == "" {
			return fmt.Errorf("model_routes[%d].from is required", i)
		}
		if len(r.To) == 0 {
			return fmt.Errorf("model_routes[%d].to is required", i)
		}
	}
	return nil
}

// ResolveModel maps an incoming model name through model_routes.
// Returns the list of target model names. If no route matches, returns the default list.
func (c *Config) ResolveModel(incoming string) []string {
	for _, r := range c.ModelRoutes.Routes {
		if matchPattern(r.From, incoming) {
			return r.To
		}
	}
	if len(c.ModelRoutes.Default) > 0 {
		return c.ModelRoutes.Default
	}
	return nil // no match, no default
}

func matchPattern(pattern, name string) bool {
	if pattern == "*" || pattern == "all" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == name
	}
	prefix, suffix, hasSuffix := strings.Cut(pattern, "*")
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if hasSuffix && !strings.HasSuffix(name, suffix) {
		return false
	}
	return true
}

// EndpointForModel finds an endpoint that allows the given target model, and returns
// a copy with the resolved model name.
func (c *Config) EndpointsForModel(targetModels []string) []EndpointModelPair {
	var pairs []EndpointModelPair
	for _, targetModel := range targetModels {
		for _, ep := range c.Endpoints {
			if ep.Enabled && ep.AllowsModel(targetModel) {
				pairs = append(pairs, EndpointModelPair{
					Endpoint: ep,
					Model:    targetModel,
				})
			}
		}
	}
	return pairs
}

type EndpointModelPair struct {
	Endpoint EndpointConfig
	Model    string
}

// RequestTokenEstimate holds rough token estimates for a request.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return len(text)
}

// ReadFile is a convenience wrapper.
func ReadFile(path string) ([]byte, error) {
	return os.ReadFile(filepath.Clean(path))
}
