package proxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"miniroute/internal/config"
	"miniroute/internal/cooldown"
)

func TestParseAnthropicReqMeta(t *testing.T) {
	model, stream := parseAnthropicReqMeta([]byte(`{"model":"claude-3-7-sonnet","stream":true}`))
	if model != "claude-3-7-sonnet" {
		t.Fatalf("unexpected model: %s", model)
	}
	if !stream {
		t.Fatal("expected stream=true")
	}
}

func TestRewriteModel(t *testing.T) {
	out := rewriteModel([]byte(`{"model":"old","messages":[]}`), "new")
	model, _ := parseAnthropicReqMeta(out)
	if model != "new" {
		t.Fatalf("expected rewritten model new, got %s", model)
	}
}

func TestClassifyErr(t *testing.T) {
	if got := classifyErr(context.Canceled); got != "client_cancelled" {
		t.Fatalf("unexpected classify canceled: %s", got)
	}
	if got := classifyErr(context.DeadlineExceeded); got != "connect_timeout" {
		t.Fatalf("unexpected classify deadline: %s", got)
	}
	if got := classifyErr(errors.New("x")); got != "internal_error" {
		t.Fatalf("unexpected classify default: %s", got)
	}
}

func TestSequentialSelectByRank(t *testing.T) {
	ct := cooldown.NewTracker()
	h := &Handler{
		cfg: &config.Config{Policy: config.PolicyConfig{Scheduler: "sequential"}},
		ct:  ct,
	}
	endpoints := []config.EndpointConfig{
		{Name: "a", Rank: 1, Enabled: true, AllowModel: []string{"m1"}},
		{Name: "b", Rank: 1, Enabled: true, AllowModel: []string{"m1"}},
		{Name: "c", Rank: 2, Enabled: true, AllowModel: []string{"m1"}},
	}
	h.cfg.Endpoints = endpoints

	pairs := h.selectEndpointPairs([]string{"m1"})
	if len(pairs) != 3 {
		t.Fatalf("expected 3 pairs, got %d", len(pairs))
	}
	if pairs[0].Endpoint.Name != "a" || pairs[1].Endpoint.Name != "b" {
		t.Fatalf("unexpected order: %s, %s", pairs[0].Endpoint.Name, pairs[1].Endpoint.Name)
	}
	if pairs[2].Endpoint.Name != "c" {
		t.Fatalf("rank 2 should be last: %s", pairs[2].Endpoint.Name)
	}
}

func TestSequentialSkipsCooldown(t *testing.T) {
	ct := cooldown.NewTracker()
	ct.RegisterProvider("MiniMax", cooldown.NewMiniMaxCooldown())
	h := &Handler{
		cfg: &config.Config{Policy: config.PolicyConfig{Scheduler: "sequential"}},
		ct:  ct,
	}
	endpoints := []config.EndpointConfig{
		{Name: "a", Rank: 1, Enabled: true, Provider: "MiniMax", AllowModel: []string{"m1"}},
		{Name: "b", Rank: 1, Enabled: true, Provider: "MiniMax", AllowModel: []string{"m1"}},
		{Name: "c", Rank: 2, Enabled: true, Provider: "GLM", AllowModel: []string{"m1"}},
	}
	h.cfg.Endpoints = endpoints

	ct.SetCooldown("a", "MiniMax", 429, []byte(`{"error":{"code":1002}}`))
	if ct.IsAvailable("a") {
		t.Fatal("expected a to be in cooldown")
	}

	pairs := h.selectEndpointPairs([]string{"m1"})
	if pairs[0].Endpoint.Name != "b" {
		t.Fatalf("expected b first (a is cooling), got %s", pairs[0].Endpoint.Name)
	}
}

func TestSequentialAllCooldownReturnsEmpty(t *testing.T) {
	ct := cooldown.NewTracker()
	ct.RegisterProvider("MiniMax", cooldown.NewMiniMaxCooldown())
	h := &Handler{
		cfg: &config.Config{Policy: config.PolicyConfig{Scheduler: "sequential"}},
		ct:  ct,
	}
	h.cfg.Endpoints = []config.EndpointConfig{
		{Name: "a", Rank: 1, Enabled: true, Provider: "MiniMax", AllowModel: []string{"m1"}},
		{Name: "b", Rank: 2, Enabled: true, Provider: "MiniMax", AllowModel: []string{"m1"}},
	}
	ct.SetCooldown("a", "MiniMax", 429, []byte(`{"error":{"code":1002}}`))
	ct.SetCooldown("b", "MiniMax", 429, []byte(`{"error":{"code":1002}}`))

	pairs := h.selectEndpointPairs([]string{"m1"})
	if len(pairs) != 0 {
		t.Fatalf("expected no pairs when all endpoints are cooling, got %d", len(pairs))
	}
}

func TestRandomSkipsCooldownEndpoints(t *testing.T) {
	ct := cooldown.NewTracker()
	ct.RegisterProvider("MiniMax", cooldown.NewMiniMaxCooldown())
	h := &Handler{
		cfg: &config.Config{Policy: config.PolicyConfig{Scheduler: "random"}},
		ct:  ct,
	}
	h.cfg.Endpoints = []config.EndpointConfig{
		{Name: "a", Rank: 1, Enabled: true, Provider: "MiniMax", AllowModel: []string{"m1"}},
		{Name: "b", Rank: 1, Enabled: true, Provider: "MiniMax", AllowModel: []string{"m1"}},
	}
	ct.SetCooldown("a", "MiniMax", 429, []byte(`{"error":{"code":1002}}`))

	pairs := h.selectEndpointPairs([]string{"m1"})
	if len(pairs) != 1 || pairs[0].Endpoint.Name != "b" {
		t.Fatalf("expected only non-cooling endpoint b, got %+v", pairs)
	}
}

func TestGroupPairsByRank(t *testing.T) {
	h := &Handler{}
	pairs := []config.EndpointModelPair{
		{Endpoint: config.EndpointConfig{Name: "a", Rank: 1}, Model: "m1"},
		{Endpoint: config.EndpointConfig{Name: "b", Rank: 2}, Model: "m1"},
		{Endpoint: config.EndpointConfig{Name: "c", Rank: 1}, Model: "m1"},
	}
	groups := h.groupPairsByRank(pairs)
	if len(groups[1]) != 2 {
		t.Fatalf("expected 2 pairs in rank 1, got %d", len(groups[1]))
	}
	if len(groups[2]) != 1 {
		t.Fatalf("expected 1 pair in rank 2, got %d", len(groups[2]))
	}
}

func TestGroupPairsByRankUsesAltRankInPeakHours(t *testing.T) {
	h := &Handler{
		now: func() time.Time {
			return time.Date(2026, 4, 23, 15, 0, 0, 0, time.Local)
		},
	}
	pairs := []config.EndpointModelPair{
		{Endpoint: config.EndpointConfig{Name: "a", Rank: 3, AltRank: 1}, Model: "m1"},
	}
	groups := h.groupPairsByRank(pairs)
	if len(groups[1]) != 1 {
		t.Fatalf("expected endpoint to use alt_rank=1 in peak hours, got groups=%v", groups)
	}
	if len(groups[3]) != 0 {
		t.Fatalf("expected endpoint not to stay in original rank during peak, got groups=%v", groups)
	}
}

func TestGroupPairsByRankFallsBackToRankWhenAltRankUnset(t *testing.T) {
	h := &Handler{
		now: func() time.Time {
			return time.Date(2026, 4, 23, 16, 0, 0, 0, time.Local)
		},
	}
	pairs := []config.EndpointModelPair{
		{Endpoint: config.EndpointConfig{Name: "a", Rank: 2}, Model: "m1"},
	}
	groups := h.groupPairsByRank(pairs)
	if len(groups[2]) != 1 {
		t.Fatalf("expected endpoint to fall back to rank=2 when alt_rank is unset, got groups=%v", groups)
	}
}

func TestRetryableStatusIncludes404(t *testing.T) {
	if !retryableStatus(404) {
		t.Fatal("expected 404 to be retryable for fallback")
	}
}

func TestCooldownTrackerIntegration(t *testing.T) {
	ct := cooldown.NewTracker()
	ct.RegisterProvider("MiniMax", cooldown.NewMiniMaxCooldown())

	if !ct.IsAvailable("test-ep") {
		t.Fatal("expected endpoint to be initially available")
	}

	ct.SetCooldown("test-ep", "MiniMax", 429, []byte(`{"base_resp":{"status_code":1002}}`))
	if ct.IsAvailable("test-ep") {
		t.Fatal("expected endpoint to be in cooldown after 1002")
	}

	ct.ClearCooldown("test-ep")
	if !ct.IsAvailable("test-ep") {
		t.Fatal("expected endpoint to be available after clear")
	}
}

func TestMiniMaxError2056LongCooldown(t *testing.T) {
	ct := cooldown.NewTracker()
	ct.RegisterProvider("MiniMax", cooldown.NewMiniMaxCooldown())

	ct.SetCooldown("test-ep", "MiniMax", 429, []byte(`{"base_resp":{"status_code":2056}}`))
	state := ct.GetState("test-ep")

	remaining := state.CooldownRemaining()
	// 2056 should give at least 1 minute buffer, and at most 5 hours
	if remaining < 1*time.Minute {
		t.Fatalf("expected at least 1min cooldown for 2056, got %v", remaining)
	}
	if remaining > 5*time.Hour {
		t.Fatalf("cooldown for 2056 should not exceed 5 hours, got %v", remaining)
	}
}

func TestModelRouteWildcard(t *testing.T) {
	cfg := &config.Config{
		ModelRoutes: config.ModelRoutesConfig{
			Default: []string{"MiniMax-M2.7"},
			Routes: []config.ModelRoute{
				{From: "claude-sonnet-*", To: []string{"MiniMax-M2.7", "GLM-5.1"}},
				{From: "claude-haiku-*", To: []string{"GLM-4.5-air"}},
			},
		},
	}

	sonnet := cfg.ResolveModel("claude-sonnet-4-6")
	if len(sonnet) != 2 || sonnet[0] != "MiniMax-M2.7" || sonnet[1] != "GLM-5.1" {
		t.Fatalf("unexpected sonnet targets: %v", sonnet)
	}

	haiku := cfg.ResolveModel("claude-haiku-4-5-20251001")
	if len(haiku) != 1 || haiku[0] != "GLM-4.5-air" {
		t.Fatalf("unexpected haiku targets: %v", haiku)
	}

	unknown := cfg.ResolveModel("some-unknown-model")
	if len(unknown) != 1 || unknown[0] != "MiniMax-M2.7" {
		t.Fatalf("unknown model should use default: %v", unknown)
	}
}

func TestEndpointAllowsModel(t *testing.T) {
	ep := config.EndpointConfig{Name: "test", AllowModel: []string{"GLM-5.1", "GLM-4.5-air"}}
	if !ep.AllowsModel("GLM-5.1") {
		t.Fatal("expected GLM-5.1 to be allowed")
	}
	if !ep.AllowsModel("GLM-4.5-air") {
		t.Fatal("expected GLM-4.5-air to be allowed")
	}
	if ep.AllowsModel("MiniMax-M2.7") {
		t.Fatal("MiniMax-M2.7 should NOT be allowed")
	}
}

func TestEstimateTokens(t *testing.T) {
	if config.EstimateTokens("hello world") != 2 {
		t.Fatalf("expected ~2 tokens for 'hello world', got %d", config.EstimateTokens("hello world"))
	}
	if config.EstimateTokens("") != 0 {
		t.Fatal("empty string should be 0")
	}
}
