package adminapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"miniroute/internal/config"
	"miniroute/internal/cooldown"
	"miniroute/internal/query"
)

type Handler struct {
	query   *query.Service
	cfg     *config.Reloader
	tracker *cooldown.Tracker
}

func New(q *query.Service, cfg *config.Reloader, tracker *cooldown.Tracker) *Handler {
	return &Handler{query: q, cfg: cfg, tracker: tracker}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/status", h.handleStatus)
	mux.HandleFunc("/api/requests", h.handleListRequests)
	mux.HandleFunc("/api/requests/", h.handleGetRequest)
	mux.HandleFunc("/api/endpoints", h.handleEndpoints)
	mux.HandleFunc("/api/dashboard", h.handleDashboard)
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	v, err := h.query.Status(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) handleListRequests(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil {
			limit = n
		}
	}
	items, err := h.query.ListRequests(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetRequest(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/download") {
		h.handleDownloadRequest(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/requests/")
	if id == "" {
		http.Error(w, "request id required", http.StatusBadRequest)
		return
	}
	item, err := h.query.GetRequest(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handler) handleDownloadRequest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/requests/")
	id = strings.TrimSuffix(id, "/download")
	id = strings.TrimSuffix(id, "/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "request id required", http.StatusBadRequest)
		return
	}

	item, err := h.query.GetRequest(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	reqSummary := parseSummaryJSON(item.Request.RequestSummary)
	respSummary := parseSummaryJSON(item.Request.ResponseSummary)
	requestPayload := firstNonEmptyString(reqSummary["payload_full"], reqSummary["payload_preview"])
	responsePayload := firstNonEmptyString(respSummary["payload_full"], respSummary["payload_preview"])

	filename := "request-" + id + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"exported_at":       time.Now().Format(time.RFC3339),
		"request_id":        id,
		"request":           item.Request,
		"attempts":          item.Attempts,
		"request_summary":   reqSummary,
		"response_summary":  respSummary,
		"request_payload":   requestPayload,
		"response_payload":  responsePayload,
		"preview_truncated": (reqSummary["preview_truncated"] == true) || (respSummary["preview_truncated"] == true),
		"note":              "Payload fields prefer stored full payload (`payload_full`) and fall back to preview when unavailable.",
	})
}

func parseSummaryJSON(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{"raw": raw}
	}
	return out
}

func firstNonEmptyString(vs ...any) string {
	for _, v := range vs {
		s, ok := v.(string)
		if ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func (h *Handler) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	states := h.tracker.GetAllStates()
	eps, err := h.query.EndpointStatuses(r.Context(), h.cfg.Get().Endpoints, states)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hour := time.Now().Hour()
	isPeak := hour >= 14 && hour < 18
	writeJSON(w, http.StatusOK, map[string]any{"items": eps, "is_peak": isPeak})
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	states := h.tracker.GetAllStates()
	overview, err := h.query.DashboardOverview(r.Context(), h.cfg.Get().Endpoints, states)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
