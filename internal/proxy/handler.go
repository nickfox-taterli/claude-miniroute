package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptrace"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"miniroute/internal/config"
	"miniroute/internal/cooldown"
	"miniroute/internal/model"
	"miniroute/internal/store/sqlite"

	"github.com/google/uuid"
)

const payloadPreviewLimit = 4096
const payloadCaptureLimit = 2 * 1024 * 1024

type Handler struct {
	cfg      *config.Reloader
	store    *sqlite.Store
	ct       *cooldown.Tracker
	clientIP func(*http.Request) string
	now      func() time.Time
	inflight atomic.Int64
	rr       sync.Map // rank (int) -> *atomic.Uint64 for round-robin
}

func New(cfg *config.Reloader, store *sqlite.Store, ct *cooldown.Tracker) *Handler {
	return &Handler{cfg: cfg, store: store, ct: ct, clientIP: defaultClientIP, now: time.Now}
}

func (h *Handler) Inflight() int64 { return h.inflight.Load() }

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1/", http.HandlerFunc(h.handleAnthropicProxy))
	return mux
}

func (h *Handler) handleAnthropicProxy(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	h.inflight.Add(1)
	defer h.inflight.Add(-1)

	requestID := "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	stream := false
	requestedModel := ""
	resolvedModel := ""

	bodyBytes, truncated, err := readBodyForRouting(r, h.cfg.Get().Storage.MaxParseBodyBytes)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	inputTokens := config.EstimateTokens(string(bodyBytes))

	if len(bodyBytes) > 0 {
		requestedModel, stream = parseAnthropicReqMeta(bodyBytes)
		resolvedModel = requestedModel
	}

	// Resolve target models through model_routes
	targetModels := h.cfg.Get().ResolveModel(resolvedModel)

	clientApp := r.Header.Get("User-Agent")
	reqPreview, reqPreviewTruncated := summarizePayload(bodyBytes, payloadPreviewLimit)
	reqFull, reqFullTruncated := summarizePayload(bodyBytes, payloadCaptureLimit)
	summary := map[string]any{
		"body_bytes":             len(bodyBytes),
		"truncated":              truncated,
		"stream":                 stream,
		"payload_preview":        reqPreview,
		"preview_truncated":      reqPreviewTruncated,
		"payload_max_bytes":      payloadPreviewLimit,
		"payload_full":           reqFull,
		"payload_full_truncated": reqFullTruncated,
		"payload_full_max_bytes": payloadCaptureLimit,
	}
	summaryJSON, _ := json.Marshal(summary)

	reqRec := model.RequestRecord{
		RequestID:      requestID,
		Protocol:       "anthropic",
		Method:         r.Method,
		Path:           r.URL.Path,
		RequestedModel: requestedModel,
		ResolvedModel:  resolvedModel,
		ClientIP:       h.clientIP(r),
		ClientApp:      clientApp,
		StartTime:      started,
		Streaming:      stream,
		UsageStatus:    "estimated",
		InputTokens:    &inputTokens,
		RequestSummary: string(summaryJSON),
	}
	log.Printf("proxy request_started request_id=%s method=%s path=%s client_ip=%s requested_model=%s resolved_model=%s target_models=%v stream=%t input_tokens_est=%d",
		requestID, r.Method, r.URL.Path, reqRec.ClientIP, requestedModel, resolvedModel, targetModels, stream, inputTokens)

	// Get endpoints filtered by allow_model for the resolved target models
	pairs := h.selectEndpointPairs(targetModels)
	if len(pairs) == 0 {
		h.finishWithError(r.Context(), w, &reqRec, started, "bad_request", "no available endpoints for model "+resolvedModel, http.StatusBadGateway)
		return
	}
	reqRec.RouteName = "default"
	log.Printf("proxy endpoints_selected request_id=%s count=%d scheduler=%s retry=%d",
		requestID, len(pairs), h.cfg.Get().Policy.Scheduler, h.cfg.Get().Policy.Retry)

	_ = h.store.InsertRequest(r.Context(), reqRec)

	var finalStatus int
	var finalTTFT int64
	var finalRespBody []byte
	var firstErr error
	retryCount := 0
	fallbackCount := 0
	maxRetries := h.cfg.Get().Policy.Retry

	attemptNo := 0
	for beIdx, pair := range pairs {
		isFallback := beIdx > 0
		if isFallback {
			fallbackCount++
		}
		for retryNo := 0; retryNo <= maxRetries; retryNo++ {
			attemptNo++
			attemptStart := time.Now()
			modelForEndpoint := pair.Model
			log.Printf("proxy attempt_started request_id=%s attempt=%d endpoint=%s upstream_path=%s retry=%t fallback=%t model=%s",
				requestID, attemptNo, pair.Endpoint.Name, r.URL.Path, retryNo > 0, isFallback, modelForEndpoint)

			attempt, status, ttft, shouldRetry, finished, respBody, err := h.callUpstream(r.Context(), r, pair.Endpoint, bodyBytes, modelForEndpoint)
			attempt.RequestID = requestID
			attempt.AttemptNo = attemptNo
			attempt.StartTime = attemptStart
			attempt.EndpointName = pair.Endpoint.Name
			attempt.EndpointURL = pair.Endpoint.BaseURL()
			attempt.WasRetry = retryNo > 0
			attempt.WasFallback = isFallback
			if ttft > 0 {
				finalTTFT = ttft
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}

			// Any upstream failure should trigger cooldown.
			if !attempt.Success {
				h.ct.SetCooldown(pair.Endpoint.Name, pair.Endpoint.Provider, status, respBody)
			}

			// Estimate output tokens from response body if available
			if len(respBody) > 0 {
				estOutput := config.EstimateTokens(string(respBody))
				attempt.OutputTokens = &estOutput
				totalEst := inputTokens + estOutput
				attempt.TotalTokens = &totalEst
			}

			_ = h.store.InsertAttempt(r.Context(), attempt)
			log.Printf("proxy attempt_finished request_id=%s attempt=%d endpoint=%s status=%d success=%t retryable=%t finished=%t error_type=%s",
				requestID, attemptNo, pair.Endpoint.Name, statusOrZero(attempt.StatusCode), attempt.Success, shouldRetry, finished, attempt.ErrorType)

			if finished {
				finalStatus = status
				finalRespBody = respBody
				reqRec.SelectedEP = pair.Endpoint.Name
				reqRec.FinalAttemptNo = attemptNo
				reqRec.ResolvedModel = modelForEndpoint
				if attempt.Success {
					reqRec.Success = true
					reqRec.StatusCode = &status
					reqRec.UsageStatus = "complete"
					h.ct.ClearCooldown(pair.Endpoint.Name)

					// Estimate output tokens from response body
					if len(respBody) > 0 {
						estOut := config.EstimateTokens(string(respBody))
						reqRec.OutputTokens = &estOut
						total := inputTokens + estOut
						reqRec.TotalTokens = &total
					}
				} else {
					reqRec.Success = false
					reqRec.StatusCode = &status
					reqRec.ErrorType = attempt.ErrorType
					reqRec.ErrorMessage = attempt.ErrorMessage
				}
				break
			}

			if shouldRetry && retryNo < maxRetries {
				retryCount++
				continue
			}
			break
		}
		if reqRec.StatusCode != nil {
			break
		}
	}

	if reqRec.StatusCode == nil {
		msg := "upstream request failed"
		if firstErr != nil {
			msg = firstErr.Error()
		}
		h.finishWithError(r.Context(), w, &reqRec, started, "upstream_5xx", msg, http.StatusBadGateway)
		// log.Printf("proxy request_finished request_id=%s status=%d success=false error_type=upstream_5xx", requestID, http.StatusBadGateway)
		return
	}

	end := time.Now()
	lat := end.Sub(started).Milliseconds()
	reqRec.EndTime = &end
	reqRec.LatencyMS = &lat
	reqRec.RetryCount = retryCount
	reqRec.FallbackCount = fallbackCount
	if finalTTFT > 0 {
		reqRec.TTFTMS = &finalTTFT
	}
	respPreview, respPreviewTruncated := summarizePayload(finalRespBody, payloadPreviewLimit)
	respFull, respFullTruncated := summarizePayload(finalRespBody, payloadCaptureLimit)
	respSummary, _ := json.Marshal(map[string]any{
		"status_code":            finalStatus,
		"payload_preview":        respPreview,
		"preview_truncated":      respPreviewTruncated,
		"payload_max_bytes":      payloadPreviewLimit,
		"payload_full":           respFull,
		"payload_full_truncated": respFullTruncated,
		"payload_full_max_bytes": payloadCaptureLimit,
	})
	reqRec.ResponseSummary = string(respSummary)
	_ = h.store.FinalizeRequest(r.Context(), reqRec)
	// log.Printf("proxy request_finished request_id=%s status=%d success=%t endpoint=%s attempts=%d retries=%d fallbacks=%d latency_ms=%d",
		// requestID, ptrInt(reqRec.StatusCode), reqRec.Success, reqRec.SelectedEP, reqRec.FinalAttemptNo, reqRec.RetryCount, reqRec.FallbackCount, lat)
}

func (h *Handler) finishWithError(ctx context.Context, w http.ResponseWriter, reqRec *model.RequestRecord, start time.Time, errType, msg string, status int) {
	reqRec.Success = false
	reqRec.ErrorType = errType
	reqRec.ErrorMessage = msg
	reqRec.StatusCode = &status
	end := time.Now()
	lat := end.Sub(start).Milliseconds()
	reqRec.EndTime = &end
	reqRec.LatencyMS = &lat
	_ = h.store.FinalizeRequest(ctx, *reqRec)
	http.Error(w, msg, status)
}

func (h *Handler) callUpstream(ctx context.Context, inbound *http.Request, ep config.EndpointConfig, rawBody []byte, resolvedModel string) (model.AttemptRecord, int, int64, bool, bool, []byte, error) {
	attempt := model.AttemptRecord{}
	start := time.Now()
	proxyPath := inbound.URL.Path
	upstreamURL := strings.TrimRight(ep.BaseURL(), "/") + proxyPath
	log.Printf("proxy upstream_request method=%s url=%s endpoint=%s", inbound.Method, upstreamURL, ep.Name)

	payload := rawBody
	if len(rawBody) > 0 && resolvedModel != "" {
		payload = rewriteModel(rawBody, resolvedModel)
	}

	upReq, err := http.NewRequestWithContext(ctx, inbound.Method, upstreamURL, bytes.NewReader(payload))
	if err != nil {
		attempt.ErrorType = "internal_error"
		attempt.ErrorMessage = err.Error()
		end := time.Now()
		attempt.EndTime = &end
		lat := end.Sub(start).Milliseconds()
		attempt.LatencyMS = &lat
		return attempt, 0, 0, false, false, nil, err
	}
	copyHeaders(upReq.Header, inbound.Header)
	// Request plain upstream body so preview/storage/frontend remain readable.
	upReq.Header.Del("Accept-Encoding")
	upReq.Header.Set("Accept-Encoding", "identity")
	applyAuth(upReq.Header, ep)
	upReq.Header.Set("X-Request-Id", inbound.Header.Get("X-Request-Id"))
	if upReq.Header.Get("Content-Type") == "" {
		upReq.Header.Set("Content-Type", "application/json")
	}
	upReq.Header.Set("anthropic-version", "2023-06-01")

	var ttftMS int64
	var firstByteAt time.Time
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() {
		firstByteAt = time.Now()
		ttftMS = firstByteAt.Sub(start).Milliseconds()
	}}
	upReq = upReq.WithContext(httptrace.WithClientTrace(upReq.Context(), trace))

	timeout := time.Duration(h.cfg.Get().Server.RequestTimeoutMS) * time.Millisecond
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(upReq)
	if err != nil {
		attempt.ErrorType = classifyErr(err)
		attempt.ErrorMessage = err.Error()
		attempt.Timeout = errors.Is(err, context.DeadlineExceeded)
		end := time.Now()
		attempt.EndTime = &end
		lat := end.Sub(start).Milliseconds()
		attempt.LatencyMS = &lat
		return attempt, 0, ttftMS, true, false, nil, err
	}
	defer resp.Body.Close()

	attempt.StatusCode = sqlite.Ptr(resp.StatusCode)
	if !firstByteAt.IsZero() {
		attempt.FirstResponseAt = &firstByteAt
		attempt.TTFTMS = &ttftMS
	}
	attempt.ResponseHeaders = sqlite.MarshalHeaders(resp.Header)

	if retryableStatus(resp.StatusCode) {
		respBody, _ := io.ReadAll(resp.Body)
		previewBody := decodeBodyForPreview(respBody, resp.Header.Get("Content-Encoding"), payloadCaptureLimit)
		attempt.Success = false
		attempt.ErrorType = classifyStatus(resp.StatusCode)
		attempt.ErrorMessage = summarizeUpstreamError(resp.Status, previewBody)
		end := time.Now()
		attempt.EndTime = &end
		lat := end.Sub(start).Milliseconds()
		attempt.LatencyMS = &lat
		return attempt, resp.StatusCode, ttftMS, true, false, previewBody, nil
	}

	w, ok := inbound.Context().Value(writerCtxKey{}).(http.ResponseWriter)
	if !ok {
		attempt.ErrorType = "internal_error"
		attempt.ErrorMessage = "missing response writer"
		return attempt, 0, ttftMS, false, false, nil, errors.New("missing writer")
	}

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		if len(respBody) > 0 {
			if _, err := w.Write(respBody); err != nil {
				previewBody := decodeBodyForPreview(respBody, resp.Header.Get("Content-Encoding"), payloadCaptureLimit)
				attempt.Success = false
				attempt.ErrorType = classifyErr(err)
				attempt.ErrorMessage = err.Error()
				end := time.Now()
				attempt.EndTime = &end
				lat := end.Sub(start).Milliseconds()
				attempt.LatencyMS = &lat
				return attempt, resp.StatusCode, ttftMS, false, true, previewBody, err
			}
		}
		previewBody := decodeBodyForPreview(respBody, resp.Header.Get("Content-Encoding"), payloadCaptureLimit)
		attempt.Success = false
		attempt.ErrorType = classifyStatus(resp.StatusCode)
		attempt.ErrorMessage = summarizeUpstreamError(resp.Status, previewBody)
		end := time.Now()
		attempt.EndTime = &end
		lat := end.Sub(start).Milliseconds()
		attempt.LatencyMS = &lat
		return attempt, resp.StatusCode, ttftMS, false, true, previewBody, nil
	}

	buf := &cappedBuffer{max: payloadCaptureLimit}
	if _, err := io.Copy(w, io.TeeReader(resp.Body, buf)); err != nil {
		previewBody := decodeBodyForPreview(buf.Bytes(), resp.Header.Get("Content-Encoding"), payloadCaptureLimit)
		attempt.Success = false
		attempt.ErrorType = classifyErr(err)
		attempt.ErrorMessage = err.Error()
		end := time.Now()
		attempt.EndTime = &end
		lat := end.Sub(start).Milliseconds()
		attempt.LatencyMS = &lat
		return attempt, resp.StatusCode, ttftMS, false, true, previewBody, err
	}

	attempt.Success = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !attempt.Success {
		attempt.ErrorType = classifyStatus(resp.StatusCode)
		attempt.ErrorMessage = resp.Status
	}
	end := time.Now()
	attempt.EndTime = &end
	lat := end.Sub(start).Milliseconds()
	attempt.LatencyMS = &lat
	previewBody := decodeBodyForPreview(buf.Bytes(), resp.Header.Get("Content-Encoding"), payloadCaptureLimit)
	return attempt, resp.StatusCode, ttftMS, false, true, previewBody, nil
}

func summarizeUpstreamError(status string, body []byte) string {
	msg := strings.TrimSpace(status)
	if len(body) == 0 {
		return msg
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return msg
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 280 {
		s = s[:280] + "..."
	}
	if msg == "" {
		return s
	}
	return msg + " | " + s
}

func summarizePayload(body []byte, limit int) (string, bool) {
	if len(body) == 0 {
		return "", false
	}
	if !looksLikeText(body) {
		return "[binary payload omitted]", false
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "", false
	}
	if len(s) > limit {
		return s[:limit], true
	}
	return s, false
}

func decodeBodyForPreview(body []byte, contentEncoding string, maxDecompressed int) []byte {
	if len(body) == 0 {
		return body
	}
	encRaw := strings.ToLower(strings.TrimSpace(contentEncoding))
	if encRaw == "" || encRaw == "identity" {
		return body
	}
	encodings := strings.Split(encRaw, ",")
	decoded := body
	// Decode in reverse order for stacked encodings.
	for i := len(encodings) - 1; i >= 0; i-- {
		enc := strings.TrimSpace(encodings[i])
		switch enc {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			gr, err := gzip.NewReader(bytes.NewReader(decoded))
			if err != nil {
				return body
			}
			next, err := io.ReadAll(io.LimitReader(gr, int64(maxDecompressed)))
			_ = gr.Close()
			if err != nil || len(next) == 0 {
				return body
			}
			decoded = next
		default:
			// Unsupported encoding (for example br). Keep raw bytes.
			return body
		}
	}
	return decoded
}

func looksLikeText(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	if !utf8.Valid(b) {
		return false
	}
	printable := 0
	total := 0
	for _, r := range string(b) {
		total++
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			printable++
		}
	}
	if total == 0 {
		return false
	}
	return float64(printable)/float64(total) >= 0.85
}

type cappedBuffer struct {
	max   int
	buf   []byte
	total int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.total += len(p)
	if len(c.buf) < c.max {
		remain := c.max - len(c.buf)
		if remain > len(p) {
			remain = len(p)
		}
		c.buf = append(c.buf, p[:remain]...)
	}
	return len(p), nil
}

func (c *cappedBuffer) Bytes() []byte {
	return c.buf
}

type writerCtxKey struct{}

func WithResponseWriter(r *http.Request, w http.ResponseWriter) *http.Request {
	ctx := context.WithValue(r.Context(), writerCtxKey{}, w)
	return r.WithContext(ctx)
}

// selectEndpointPairs returns endpoint-model pairs based on model_routes and allow_model.
func (h *Handler) selectEndpointPairs(targetModels []string) []config.EndpointModelPair {
	allPairs := h.cfg.Get().EndpointsForModel(targetModels)
	if len(allPairs) == 0 {
		return nil
	}

	// Group by rank
	ranks := h.groupPairsByRank(allPairs)

	switch h.cfg.Get().Policy.Scheduler {
	case "random":
		return h.randomSelectPairs(ranks)
	default:
		return h.sequentialSelectPairs(ranks)
	}
}

func (h *Handler) groupPairsByRank(pairs []config.EndpointModelPair) map[int][]config.EndpointModelPair {
	groups := map[int][]config.EndpointModelPair{}
	now := time.Now
	if h.now != nil {
		now = h.now
	}
	peak := isPeakHours(now())
	for _, p := range pairs {
		rank := p.Endpoint.Rank
		if peak && p.Endpoint.AltRank > 0 {
			rank = p.Endpoint.AltRank
		}
		groups[rank] = append(groups[rank], p)
	}
	return groups
}

func isPeakHours(now time.Time) bool {
	hour := now.Hour()
	return hour >= 14 && hour < 18
}

func (h *Handler) sequentialSelectPairs(ranks map[int][]config.EndpointModelPair) []config.EndpointModelPair {
	sortedRanks := make([]int, 0, len(ranks))
	for r := range ranks {
		sortedRanks = append(sortedRanks, r)
	}
	sort.Ints(sortedRanks)

	var result []config.EndpointModelPair
	for _, rank := range sortedRanks {
		pairs := ranks[rank]
		available := h.filterPairsAvailable(pairs)
		if len(available) == 0 {
			continue
		}
		rotated := h.rotatePairs(rank, available)
		result = append(result, rotated...)
	}
	return result
}

func (h *Handler) randomSelectPairs(ranks map[int][]config.EndpointModelPair) []config.EndpointModelPair {
	sortedRanks := make([]int, 0, len(ranks))
	for r := range ranks {
		sortedRanks = append(sortedRanks, r)
	}
	sort.Ints(sortedRanks)

	var result []config.EndpointModelPair
	for _, rank := range sortedRanks {
		pairs := ranks[rank]
		available := h.filterPairsAvailable(pairs)
		if len(available) == 0 {
			continue
		}
		shuffled := make([]config.EndpointModelPair, len(available))
		copy(shuffled, available)
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		result = append(result, shuffled...)
	}
	return result
}

func (h *Handler) filterPairsAvailable(pairs []config.EndpointModelPair) []config.EndpointModelPair {
	var out []config.EndpointModelPair
	for _, p := range pairs {
		if h.ct.IsAvailable(p.Endpoint.Name) {
			out = append(out, p)
		}
	}
	return out
}

func (h *Handler) rotatePairs(rank int, pairs []config.EndpointModelPair) []config.EndpointModelPair {
	if len(pairs) <= 1 {
		return pairs
	}
	counterAny, _ := h.rr.LoadOrStore(rank, &atomic.Uint64{})
	counter := counterAny.(*atomic.Uint64)
	start := int(counter.Add(1)-1) % len(pairs)
	rotated := make([]config.EndpointModelPair, 0, len(pairs))
	rotated = append(rotated, pairs[start:]...)
	rotated = append(rotated, pairs[:start]...)
	return rotated
}

func readBodyForRouting(r *http.Request, limit int64) ([]byte, bool, error) {
	if r.Body == nil {
		return nil, false, nil
	}
	defer r.Body.Close()
	lr := io.LimitReader(r.Body, limit+1)
	b, err := io.ReadAll(lr)
	if err != nil {
		return nil, false, err
	}
	truncated := int64(len(b)) > limit
	if truncated {
		b = b[:limit]
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, truncated, nil
}

func parseAnthropicReqMeta(body []byte) (string, bool) {
	var p struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", false
	}
	return p.Model, p.Stream
}

func rewriteModel(body []byte, modelName string) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	payload["model"] = modelName
	b, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return b
}

func applyAuth(h http.Header, ep config.EndpointConfig) {
	// Never forward caller auth to upstream provider; always enforce endpoint key.
	h.Del("x-api-key")
	h.Del("X-API-Key")
	h.Del("authorization")
	h.Del("Authorization")
	if ep.APIKey == "" {
		return
	}
	switch ep.Provider {
	case "GLM":
		// GLM Anthropic-compatible docs use x-api-key.
		h.Set("x-api-key", ep.APIKey)
	default:
		h.Set("x-api-key", ep.APIKey)
	}
}

func retryableStatus(code int) bool {
	return code == http.StatusNotFound ||
		code == http.StatusTooManyRequests ||
		code == http.StatusRequestTimeout ||
		code == http.StatusConflict ||
		code >= 500
}

func classifyStatus(code int) string {
	switch {
	case code == 400:
		return "bad_request"
	case code == 401:
		return "auth_error"
	case code == 403:
		return "forbidden"
	case code == 429:
		return "rate_limited"
	case code >= 500:
		return "upstream_5xx"
	default:
		return "protocol_error"
	}
}

func classifyErr(err error) string {
	if errors.Is(err, context.Canceled) {
		return "client_cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "connect_timeout"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "read_timeout"
	}
	return "internal_error"
}

func copyHeaders(dst, src http.Header) {
	for k, vals := range src {
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func defaultClientIP(r *http.Request) string {
	ff := r.Header.Get("X-Forwarded-For")
	if ff != "" {
		parts := strings.Split(ff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func MiddlewareInjectWriter(next http.Handler, h *Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, WithResponseWriter(r, w))
	})
}

func statusOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func ptrInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
