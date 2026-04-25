package model

import "time"

type RequestRecord struct {
	RequestID       string     `json:"request_id"`
	Protocol        string     `json:"protocol"`
	RouteName       string     `json:"route_name"`
	Method          string     `json:"method"`
	Path            string     `json:"path"`
	RequestedModel  string     `json:"requested_model"`
	ResolvedModel   string     `json:"resolved_model"`
	ClientIP        string     `json:"client_ip"`
	ClientApp       string     `json:"client_app"`
	StartTime       time.Time  `json:"start_time"`
	EndTime         *time.Time `json:"end_time,omitempty"`
	LatencyMS       *int64     `json:"latency_ms,omitempty"`
	TTFTMS          *int64     `json:"ttft_ms,omitempty"`
	Streaming       bool       `json:"streaming"`
	StatusCode      *int       `json:"status_code,omitempty"`
	Success         bool       `json:"success"`
	ErrorType       string     `json:"error_type"`
	ErrorMessage    string     `json:"error_message"`
	RetryCount      int        `json:"retry_count"`
	FallbackCount   int        `json:"fallback_count"`
	FinalAttemptNo  int        `json:"final_attempt_no"`
	SelectedEP      string     `json:"selected_endpoint"`
	UsageStatus     string     `json:"usage_status"`
	InputTokens     *int       `json:"input_tokens,omitempty"`
	OutputTokens    *int       `json:"output_tokens,omitempty"`
	TotalTokens     *int       `json:"total_tokens,omitempty"`
	RequestSummary  string     `json:"request_summary"`
	ResponseSummary string     `json:"response_summary"`
}

type AttemptRecord struct {
	RequestID       string     `json:"request_id"`
	AttemptNo       int        `json:"attempt_no"`
	EndpointName    string     `json:"endpoint_name"`
	EndpointURL     string     `json:"endpoint_url"`
	StartTime       time.Time  `json:"start_time"`
	FirstResponseAt *time.Time `json:"first_response_at,omitempty"`
	EndTime         *time.Time `json:"end_time,omitempty"`
	LatencyMS       *int64     `json:"latency_ms,omitempty"`
	TTFTMS          *int64     `json:"ttft_ms,omitempty"`
	StatusCode      *int       `json:"status_code,omitempty"`
	Success         bool       `json:"success"`
	ErrorType       string     `json:"error_type"`
	ErrorMessage    string     `json:"error_message"`
	Timeout         bool       `json:"timeout"`
	WasRetry        bool       `json:"was_retry"`
	WasFallback     bool       `json:"was_fallback"`
	ResponseHeaders string     `json:"response_headers"`
	InputTokens     *int       `json:"input_tokens,omitempty"`
	OutputTokens    *int       `json:"output_tokens,omitempty"`
	TotalTokens     *int       `json:"total_tokens,omitempty"`
}

type RequestListItem struct {
	RequestID     string `json:"request_id"`
	Protocol      string `json:"protocol"`
	RouteName     string `json:"route_name"`
	Path          string `json:"path"`
	ResolvedModel string `json:"resolved_model"`
	StatusCode    int    `json:"status_code"`
	Success       bool   `json:"success"`
	LatencyMS     int64  `json:"latency_ms"`
	TTFTMS        int64  `json:"ttft_ms"`
	StartTS       int64  `json:"start_ts"`
	ErrorType     string `json:"error_type,omitempty"`
	InputTokens   int    `json:"input_tokens"`
	OutputTokens  int    `json:"output_tokens"`
	TotalTokens   int    `json:"total_tokens"`
}

type RequestDetail struct {
	Request  RequestRecord   `json:"request"`
	Attempts []AttemptRecord `json:"attempts"`
}

type StatusView struct {
	NowTS          int64 `json:"now_ts"`
	UptimeSec      int64 `json:"uptime_sec"`
	ProxyInflight  int64 `json:"proxy_inflight"`
	RequestsLast1h int64 `json:"requests_last_1h"`
	SuccessLast1h  int64 `json:"success_last_1h"`
	DBOpenConns    int   `json:"db_open_connections"`
	DBInUse        int   `json:"db_in_use"`
	DBIdle         int   `json:"db_idle"`
}

type EndpointStatus struct {
	Name          string  `json:"name"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
		Rank          int     `json:"rank"`
	AltRank       int     `json:"alt_rank"`
	ActiveRank    int     `json:"active_rank"`
	Enabled       bool    `json:"enabled"`
	Available     bool    `json:"available"`
	CooldownUntil *int64  `json:"cooldown_until,omitempty"`
	CooldownLeft  string  `json:"cooldown_left,omitempty"`
	ConsecErrors  int     `json:"consec_errors"`
	RecentErrors  int64   `json:"recent_errors_1h"`
	Total1h       int64   `json:"total_1h"`
	SuccessRate1h float64 `json:"success_rate_1h"`
}

type DashboardOverview struct {
	Status       StatusView       `json:"status"`
	Endpoints    []EndpointStatus `json:"endpoints"`
	IsPeak       bool             `json:"is_peak"`
	TokenUsage1h TokenUsage       `json:"token_usage_1h"`
	TokenUsage5h TokenUsage       `json:"token_usage_5h"`
	TokenToday   TokenUsage       `json:"token_usage_today"`
	TokenMonth   TokenUsage       `json:"token_usage_month"`
	TokenTotal   TokenUsage       `json:"token_usage_total"`
}

type TokenUsage struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
	Total  int64 `json:"total"`
}
