package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"miniroute/internal/model"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) DBStats() sql.DBStats { return s.db.Stats() }
func (s *Store) Close() error         { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS requests (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT NOT NULL UNIQUE,
  protocol TEXT NOT NULL,
  route_name TEXT,
  client_ip TEXT,
  client_app TEXT,
  method TEXT NOT NULL,
  path TEXT NOT NULL,
  requested_model TEXT,
  resolved_model TEXT,
  start_ts INTEGER NOT NULL,
  end_ts INTEGER,
  latency_ms INTEGER,
  ttft_ms INTEGER,
  streaming INTEGER NOT NULL DEFAULT 0,
  status_code INTEGER,
  success INTEGER NOT NULL DEFAULT 0,
  error_type TEXT,
  error_message TEXT,
  final_attempt_no INTEGER NOT NULL DEFAULT 0,
  selected_endpoint TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  fallback_count INTEGER NOT NULL DEFAULT 0,
  usage_status TEXT NOT NULL DEFAULT 'unknown',
  input_tokens INTEGER,
  output_tokens INTEGER,
  total_tokens INTEGER,
  request_summary_json TEXT,
  response_summary_json TEXT,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT NOT NULL,
  attempt_no INTEGER NOT NULL,
  endpoint_name TEXT NOT NULL,
  endpoint_url TEXT,
  was_retry INTEGER NOT NULL DEFAULT 0,
  was_fallback INTEGER NOT NULL DEFAULT 0,
  start_ts INTEGER NOT NULL,
  first_response_byte_ts INTEGER,
  end_ts INTEGER,
  latency_ms INTEGER,
  ttft_ms INTEGER,
  status_code INTEGER,
  success INTEGER NOT NULL DEFAULT 0,
  error_type TEXT,
  error_message TEXT,
  timeout INTEGER NOT NULL DEFAULT 0,
  response_headers_json TEXT,
  FOREIGN KEY(request_id) REFERENCES requests(request_id) ON DELETE CASCADE,
  UNIQUE(request_id, attempt_no)
);
CREATE INDEX IF NOT EXISTS idx_requests_start_ts ON requests(start_ts DESC);
CREATE INDEX IF NOT EXISTS idx_attempts_request_attempt ON attempts(request_id, attempt_no);
`)
	return err
}

func (s *Store) InsertRequest(ctx context.Context, r model.RequestRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO requests (
request_id, protocol, route_name, client_ip, client_app, method, path,
requested_model, resolved_model, start_ts, streaming, request_summary_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, r.RequestID, r.Protocol, r.RouteName, r.ClientIP, r.ClientApp, r.Method, r.Path,
		r.RequestedModel, r.ResolvedModel, r.StartTime.UnixMilli(), boolInt(r.Streaming), r.RequestSummary, time.Now().UnixMilli())
	return err
}

func (s *Store) InsertAttempt(ctx context.Context, a model.AttemptRecord) error {
	var firstRespTS any
	if a.FirstResponseAt != nil {
		firstRespTS = a.FirstResponseAt.UnixMilli()
	}
	var endTS any
	if a.EndTime != nil {
		endTS = a.EndTime.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO attempts (
request_id, attempt_no, endpoint_name, endpoint_url, was_retry, was_fallback,
start_ts, first_response_byte_ts, end_ts, latency_ms, ttft_ms, status_code, success,
error_type, error_message, timeout, response_headers_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, a.RequestID, a.AttemptNo, a.EndpointName, a.EndpointURL, boolInt(a.WasRetry), boolInt(a.WasFallback),
		a.StartTime.UnixMilli(), firstRespTS, endTS, a.LatencyMS, a.TTFTMS, a.StatusCode,
		boolInt(a.Success), a.ErrorType, a.ErrorMessage, boolInt(a.Timeout), a.ResponseHeaders)
	return err
}

func (s *Store) FinalizeRequest(ctx context.Context, r model.RequestRecord) error {
	var endTS any
	if r.EndTime != nil {
		endTS = r.EndTime.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE requests SET
end_ts=?, latency_ms=?, ttft_ms=?, status_code=?, success=?, error_type=?, error_message=?,
final_attempt_no=?, selected_endpoint=?, retry_count=?, fallback_count=?, usage_status=?,
input_tokens=?, output_tokens=?, total_tokens=?, response_summary_json=?
WHERE request_id=?
`, endTS, r.LatencyMS, r.TTFTMS, r.StatusCode, boolInt(r.Success), nullIfEmpty(r.ErrorType), nullIfEmpty(r.ErrorMessage),
		r.FinalAttemptNo, nullIfEmpty(r.SelectedEP), r.RetryCount, r.FallbackCount, r.UsageStatus,
		r.InputTokens, r.OutputTokens, r.TotalTokens, r.ResponseSummary, r.RequestID)
	return err
}

func (s *Store) ListRequests(ctx context.Context, limit int) ([]model.RequestListItem, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT request_id, protocol, route_name, path, COALESCE(resolved_model,''), COALESCE(status_code,0), success,
COALESCE(latency_ms,0), COALESCE(ttft_ms,0), start_ts, COALESCE(error_type,''), COALESCE(input_tokens,0), COALESCE(output_tokens,0), COALESCE(total_tokens,0)
FROM requests ORDER BY start_ts DESC LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.RequestListItem, 0, limit)
	for rows.Next() {
		var it model.RequestListItem
		var success int
		if err := rows.Scan(&it.RequestID, &it.Protocol, &it.RouteName, &it.Path, &it.ResolvedModel, &it.StatusCode, &success, &it.LatencyMS, &it.TTFTMS, &it.StartTS, &it.ErrorType, &it.InputTokens, &it.OutputTokens, &it.TotalTokens); err != nil {
			return nil, err
		}
		it.Success = success == 1
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *Store) GetRequest(ctx context.Context, requestID string) (*model.RequestDetail, error) {
	var req model.RequestRecord
	var streaming, success int
	row := s.db.QueryRowContext(ctx, `
SELECT request_id, protocol, route_name, method, path, COALESCE(requested_model,''), COALESCE(resolved_model,''),
COALESCE(client_ip,''), COALESCE(client_app,''), start_ts, end_ts, COALESCE(latency_ms,0), COALESCE(ttft_ms,0),
streaming, status_code, success, COALESCE(error_type,''), COALESCE(error_message,''), retry_count, fallback_count,
final_attempt_no, COALESCE(selected_endpoint,''), usage_status, input_tokens, output_tokens, total_tokens,
COALESCE(request_summary_json,'{}'), COALESCE(response_summary_json,'{}')
FROM requests WHERE request_id=?
`, requestID)
	var startTS int64
	var endTS sql.NullInt64
	var latency, ttft sql.NullInt64
	var status sql.NullInt64
	var inTok, outTok, totalTok sql.NullInt64
	if err := row.Scan(&req.RequestID, &req.Protocol, &req.RouteName, &req.Method, &req.Path, &req.RequestedModel, &req.ResolvedModel,
		&req.ClientIP, &req.ClientApp, &startTS, &endTS, &latency, &ttft, &streaming, &status, &success,
		&req.ErrorType, &req.ErrorMessage, &req.RetryCount, &req.FallbackCount, &req.FinalAttemptNo, &req.SelectedEP,
		&req.UsageStatus, &inTok, &outTok, &totalTok, &req.RequestSummary, &req.ResponseSummary); err != nil {
		return nil, err
	}
	req.StartTime = time.UnixMilli(startTS)
	req.Streaming = streaming == 1
	req.Success = success == 1
	if endTS.Valid {
		t := time.UnixMilli(endTS.Int64)
		req.EndTime = &t
	}
	if latency.Valid {
		v := latency.Int64
		req.LatencyMS = &v
	}
	if ttft.Valid {
		v := ttft.Int64
		req.TTFTMS = &v
	}
	if status.Valid {
		v := int(status.Int64)
		req.StatusCode = &v
	}
	if inTok.Valid {
		v := int(inTok.Int64)
		req.InputTokens = &v
	}
	if outTok.Valid {
		v := int(outTok.Int64)
		req.OutputTokens = &v
	}
	if totalTok.Valid {
		v := int(totalTok.Int64)
		req.TotalTokens = &v
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT attempt_no, endpoint_name, COALESCE(endpoint_url,''), start_ts, first_response_byte_ts, end_ts,
latency_ms, ttft_ms, status_code, success, COALESCE(error_type,''), COALESCE(error_message,''), timeout,
was_retry, was_fallback, COALESCE(response_headers_json,'{}')
FROM attempts WHERE request_id=? ORDER BY attempt_no ASC
`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attempts := make([]model.AttemptRecord, 0)
	for rows.Next() {
		var a model.AttemptRecord
		var start, first, end sql.NullInt64
		var lat, ttftv sql.NullInt64
		var statusv sql.NullInt64
		var succ, timeout, wasRetry, wasFallback int
		if err := rows.Scan(&a.AttemptNo, &a.EndpointName, &a.EndpointURL, &start, &first, &end, &lat, &ttftv, &statusv, &succ,
			&a.ErrorType, &a.ErrorMessage, &timeout, &wasRetry, &wasFallback, &a.ResponseHeaders); err != nil {
			return nil, err
		}
		a.RequestID = requestID
		if start.Valid {
			a.StartTime = time.UnixMilli(start.Int64)
		}
		if first.Valid {
			t := time.UnixMilli(first.Int64)
			a.FirstResponseAt = &t
		}
		if end.Valid {
			t := time.UnixMilli(end.Int64)
			a.EndTime = &t
		}
		if lat.Valid {
			v := lat.Int64
			a.LatencyMS = &v
		}
		if ttftv.Valid {
			v := ttftv.Int64
			a.TTFTMS = &v
		}
		if statusv.Valid {
			v := int(statusv.Int64)
			a.StatusCode = &v
		}
		a.Success = succ == 1
		a.Timeout = timeout == 1
		a.WasRetry = wasRetry == 1
		a.WasFallback = wasFallback == 1
		attempts = append(attempts, a)
	}
	return &model.RequestDetail{Request: req, Attempts: attempts}, rows.Err()
}

func (s *Store) StatusView(ctx context.Context, startedAt time.Time, inflight int64) (model.StatusView, error) {
	now := time.Now()
	var total, success int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN success=1 THEN 1 ELSE 0 END),0) FROM requests WHERE start_ts >= ?`, now.Add(-time.Hour).UnixMilli()).Scan(&total, &success); err != nil {
		return model.StatusView{}, err
	}
	stats := s.db.Stats()
	return model.StatusView{
		NowTS:          now.Unix(),
		UptimeSec:      int64(now.Sub(startedAt).Seconds()),
		ProxyInflight:  inflight,
		RequestsLast1h: total,
		SuccessLast1h:  success,
		DBOpenConns:    stats.OpenConnections,
		DBInUse:        stats.InUse,
		DBIdle:         stats.Idle,
	}, nil
}

func (s *Store) EndpointStats1h(ctx context.Context, endpointName string) (total int64, success int64, err error) {
	cutoff := time.Now().Add(-time.Hour).UnixMilli()
	err = s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(CASE WHEN success=1 THEN 1 ELSE 0 END), 0)
FROM attempts WHERE endpoint_name = ? AND start_ts >= ?
`, endpointName, cutoff).Scan(&total, &success)
	return
}

func (s *Store) TokenUsageSince(ctx context.Context, sinceTS int64) (inputTokens int64, outputTokens int64, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
FROM requests WHERE start_ts >= ?
`, sinceTS).Scan(&inputTokens, &outputTokens)
	return
}

func (s *Store) TokenUsageBetween(ctx context.Context, fromTS, toTS int64) (inputTokens int64, outputTokens int64, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
FROM requests WHERE start_ts >= ? AND start_ts < ?
`, fromTS, toTS).Scan(&inputTokens, &outputTokens)
	return
}

func (s *Store) TokenUsageAll(ctx context.Context) (inputTokens int64, outputTokens int64, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
FROM requests
`).Scan(&inputTokens, &outputTokens)
	return
}

func MarshalHeaders(h map[string][]string) string {
	b, _ := json.Marshal(h)
	return string(b)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func Ptr[T any](v T) *T { return &v }

func Must(v any, err error) any {
	if err != nil {
		panic(fmt.Sprintf("must: %v", err))
	}
	return v
}
