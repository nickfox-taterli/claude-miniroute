package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndValidate(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "config.yaml")
	content := `
endpoints:
  - name: ep1
    api_key: test_key
    allow_model: ["MiniMax-M2.7"]
    provider: MiniMax
    rank: 1
    enabled: true
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Fatalf("unexpected default listen: %s", cfg.Server.Listen)
	}
	if cfg.Policy.Scheduler != "sequential" {
		t.Fatalf("unexpected default scheduler: %s", cfg.Policy.Scheduler)
	}
	if cfg.Policy.Retry != 1 {
		t.Fatalf("unexpected default retry: %d", cfg.Policy.Retry)
	}
	if cfg.Endpoints[0].BaseURL() != "https://api.minimaxi.com/anthropic" {
		t.Fatalf("unexpected MiniMax base URL: %s", cfg.Endpoints[0].BaseURL())
	}
}

func TestLoadRejectsInvalidProvider(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "config.yaml")
	content := `
endpoints:
  - name: ep1
    api_key: test_key
    allow_model: ["m1"]
    provider: InvalidProvider
    rank: 1
    enabled: true
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected validation error for invalid provider")
	}
}

func TestLoadRejectsDuplicateName(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "config.yaml")
	content := `
endpoints:
  - name: ep1
    api_key: key1
    allow_model: ["m1"]
    provider: MiniMax
    rank: 1
    enabled: true
  - name: ep1
    api_key: key2
    allow_model: ["m2"]
    provider: GLM
    rank: 2
    enabled: true
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected validation error for duplicate name")
	}
}

func TestGLMBaseURL(t *testing.T) {
	ep := EndpointConfig{Provider: "GLM"}
	if ep.BaseURL() != "https://open.bigmodel.cn/api/anthropic" {
		t.Fatalf("unexpected GLM base URL: %s", ep.BaseURL())
	}
}

func TestSiliconflowBaseURL(t *testing.T) {
	ep := EndpointConfig{Provider: "siliconflow"}
	if ep.BaseURL() != "https://api.siliconflow.cn/" {
		t.Fatalf("unexpected siliconflow base URL: %s", ep.BaseURL())
	}
}

func TestOpenRouterBaseURL(t *testing.T) {
	ep := EndpointConfig{Provider: "openrouter"}
	if ep.BaseURL() != "https://openrouter.ai/api" {
		t.Fatalf("unexpected openrouter base URL: %s", ep.BaseURL())
	}
}

func TestLoadRejectsInvalidAltRank(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "config.yaml")
	content := `
endpoints:
  - name: ep1
    api_key: test_key
    allow_model: ["m1"]
    provider: MiniMax
    rank: 1
    alt_rank: -1
    enabled: true
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected validation error for invalid alt_rank")
	}
}
