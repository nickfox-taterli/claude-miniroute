package cooldown

import (
	"sync"
	"time"
)

const fallbackCooldown = 3 * time.Minute

type ProviderCooldown interface {
	CalculateCooldown(statusCode int, body []byte) time.Duration
	DefaultCooldown() time.Duration
}

type EndpointState struct {
	CooldownUntil time.Time
	ConsecErrors  int
}

func (s EndpointState) IsAvailable() bool {
	return time.Now().After(s.CooldownUntil) || s.CooldownUntil.IsZero()
}

func (s EndpointState) CooldownRemaining() time.Duration {
	if s.CooldownUntil.IsZero() {
		return 0
	}
	d := time.Until(s.CooldownUntil)
	if d < 0 {
		return 0
	}
	return d
}

type Tracker struct {
	mu        sync.Mutex
	states    map[string]*EndpointState
	providers map[string]ProviderCooldown
}

func NewTracker() *Tracker {
	return &Tracker{
		states:    make(map[string]*EndpointState),
		providers: make(map[string]ProviderCooldown),
	}
}

func (t *Tracker) RegisterProvider(name string, pc ProviderCooldown) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.providers[name] = pc
}

func (t *Tracker) IsAvailable(endpointName string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.states[endpointName]
	if !ok {
		return true
	}
	return s.IsAvailable()
}

func (t *Tracker) SetCooldown(endpointName string, providerName string, statusCode int, body []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	s := t.getOrCreate(endpointName)
	s.ConsecErrors++

	pc, ok := t.providers[providerName]
	if !ok {
		s.CooldownUntil = time.Now().Add(fallbackCooldown)
		return
	}

	dur := pc.CalculateCooldown(statusCode, body)
	if dur <= 0 {
		dur = pc.DefaultCooldown()
	}
	if dur <= 0 {
		dur = fallbackCooldown
	}
	s.CooldownUntil = time.Now().Add(dur)
}

func (t *Tracker) ClearCooldown(endpointName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.states[endpointName]
	if !ok {
		return
	}
	s.CooldownUntil = time.Time{}
	s.ConsecErrors = 0
}

func (t *Tracker) GetState(endpointName string) EndpointState {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.states[endpointName]
	if !ok {
		return EndpointState{}
	}
	return *s
}

func (t *Tracker) GetAllStates() map[string]EndpointState {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]EndpointState, len(t.states))
	for k, v := range t.states {
		out[k] = *v
	}
	return out
}

func (t *Tracker) getOrCreate(name string) *EndpointState {
	s, ok := t.states[name]
	if !ok {
		s = &EndpointState{}
		t.states[name] = s
	}
	return s
}
