package query

import (
	"context"
	"fmt"
	"strings"
	"time"

	"miniroute/internal/config"
	"miniroute/internal/cooldown"
	"miniroute/internal/model"
	"miniroute/internal/store/sqlite"
)

type Service struct {
	store     *sqlite.Store
	startedAt time.Time
	inflight  func() int64
}

func New(store *sqlite.Store, startedAt time.Time, inflight func() int64) *Service {
	return &Service{store: store, startedAt: startedAt, inflight: inflight}
}

func (s *Service) Status(ctx context.Context) (model.StatusView, error) {
	return s.store.StatusView(ctx, s.startedAt, s.inflight())
}

func (s *Service) ListRequests(ctx context.Context, limit int) ([]model.RequestListItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	return s.store.ListRequests(ctx, limit)
}

func (s *Service) GetRequest(ctx context.Context, requestID string) (*model.RequestDetail, error) {
	return s.store.GetRequest(ctx, requestID)
}

func isPeakHours(now time.Time) bool {
	hour := now.Hour()
	return hour >= 14 && hour < 18
}

func (s *Service) EndpointStatuses(ctx context.Context, endpoints []config.EndpointConfig, states map[string]cooldown.EndpointState) ([]model.EndpointStatus, error) {
	now := time.Now()
	peak := isPeakHours(now)
	out := make([]model.EndpointStatus, 0, len(endpoints))
	for _, ep := range endpoints {
		state := states[ep.Name]
		total, success, err := s.store.EndpointStats1h(ctx, ep.Name)
		if err != nil {
			return nil, fmt.Errorf("endpoint stats for %s: %w", ep.Name, err)
		}
		var rate float64
		if total > 0 {
			rate = float64(success) / float64(total)
		}
		activeRank := ep.Rank
		if peak && ep.AltRank > 0 {
			activeRank = ep.AltRank
		}
		es := model.EndpointStatus{
			Name:          ep.Name,
			Provider:      ep.Provider,
			Model:         strings.Join(ep.AllowModel, ", "),
			Rank:          ep.Rank,
			AltRank:       ep.AltRank,
			ActiveRank:    activeRank,
			Enabled:       ep.Enabled,
			Available:     state.IsAvailable(),
			ConsecErrors:  state.ConsecErrors,
			RecentErrors:  total - success,
			Total1h:       total,
			SuccessRate1h: rate,
		}
		if !state.CooldownUntil.IsZero() {
			until := state.CooldownUntil.UnixMilli()
			es.CooldownUntil = &until
			left := state.CooldownRemaining()
			if left > 0 {
				es.CooldownLeft = formatDuration(left)
			}
		}
		out = append(out, es)
	}
	return out, nil
}

func (s *Service) DashboardOverview(ctx context.Context, endpoints []config.EndpointConfig, states map[string]cooldown.EndpointState) (model.DashboardOverview, error) {
	status, err := s.Status(ctx)
	if err != nil {
		return model.DashboardOverview{}, err
	}
	eps, err := s.EndpointStatuses(ctx, endpoints, states)
	if err != nil {
		return model.DashboardOverview{}, err
	}
	now := time.Now()
	peak := isPeakHours(now)
	loc := now.Location()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

	in1h, out1h, err := s.store.TokenUsageSince(ctx, now.Add(-time.Hour).UnixMilli())
	if err != nil {
		return model.DashboardOverview{}, err
	}
	in5h, out5h, err := s.store.TokenUsageSince(ctx, now.Add(-5*time.Hour).UnixMilli())
	if err != nil {
		return model.DashboardOverview{}, err
	}
	inToday, outToday, err := s.store.TokenUsageSince(ctx, dayStart.UnixMilli())
	if err != nil {
		return model.DashboardOverview{}, err
	}
	inMonth, outMonth, err := s.store.TokenUsageSince(ctx, monthStart.UnixMilli())
	if err != nil {
		return model.DashboardOverview{}, err
	}
	inAll, outAll, err := s.store.TokenUsageAll(ctx)
	if err != nil {
		return model.DashboardOverview{}, err
	}
	return model.DashboardOverview{
		Status:       status,
		Endpoints:    eps,
		IsPeak:       peak,
		TokenUsage1h: model.TokenUsage{Input: in1h, Output: out1h, Total: in1h + out1h},
		TokenUsage5h: model.TokenUsage{Input: in5h, Output: out5h, Total: in5h + out5h},
		TokenToday:   model.TokenUsage{Input: inToday, Output: outToday, Total: inToday + outToday},
		TokenMonth:   model.TokenUsage{Input: inMonth, Output: outMonth, Total: inMonth + outMonth},
		TokenTotal:   model.TokenUsage{Input: inAll, Output: outAll, Total: inAll + outAll},
	}, nil
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}
