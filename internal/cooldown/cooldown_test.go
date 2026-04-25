package cooldown

import (
	"testing"
	"time"
)

func TestSetCooldown_UnknownProviderUsesFallbackThreeMinutes(t *testing.T) {
	ct := NewTracker()
	ct.SetCooldown("ep-unknown", "UnknownProvider", 500, nil)

	remaining := ct.GetState("ep-unknown").CooldownRemaining()
	if remaining < 179*time.Second || remaining > 181*time.Second {
		t.Fatalf("expected ~3m cooldown for unknown provider, got %v", remaining)
	}
}

func TestSetCooldown_ProviderDefaultUsedForGenericError(t *testing.T) {
	ct := NewTracker()
	ct.RegisterProvider("MiniMax", NewMiniMaxCooldown())
	ct.SetCooldown("ep-minimax", "MiniMax", 400, nil)

	remaining := ct.GetState("ep-minimax").CooldownRemaining()
	if remaining < 179*time.Second || remaining > 181*time.Second {
		t.Fatalf("expected ~3m default cooldown for generic provider error, got %v", remaining)
	}
}
