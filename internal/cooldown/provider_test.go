package cooldown

import (
	"testing"
	"time"
)

func TestParseGLMResetTime_FromNextResetMillis(t *testing.T) {
	body := []byte(`{"error":{"nextResetTime":1776936127000}}`)
	got := parseGLMResetTime(body)
	if got.IsZero() {
		t.Fatal("expected non-zero reset time")
	}
	if got.UnixMilli() != 1776936127000 {
		t.Fatalf("unexpected millis, got=%d", got.UnixMilli())
	}
}

func TestParseGLMResetTime_FromMessageDatetime(t *testing.T) {
	body := []byte(`{"error":{"code":"1308","message":"已达到 5 小时的使用上限.您的限额将在 2026-04-23 17:22:07 重置."}}`)
	got := parseGLMResetTime(body)
	if got.IsZero() {
		t.Fatal("expected non-zero reset time from message")
	}
	if got.Year() != 2026 || got.Month() != 4 || got.Day() != 23 || got.Hour() != 17 || got.Minute() != 22 || got.Second() != 7 {
		t.Fatalf("unexpected parsed time: %v", got)
	}
}

func TestGLMCooldown_FallbackToThreeMinutesWhenResetTimeUnparseable(t *testing.T) {
	g := NewGLMCooldown()
	body := []byte(`{"error":{"code":"1308","message":"已达到使用上限，请稍后重试"}}`)
	got := g.CalculateCooldown(429, body)
	if got != 3*time.Minute {
		t.Fatalf("expected 3m fallback cooldown, got %v", got)
	}
}
