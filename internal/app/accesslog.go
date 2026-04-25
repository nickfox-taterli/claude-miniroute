package app

import (
	// "log"
	"net/http"
	// "time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func accessLogMiddleware(serverName string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// latency := time.Since(start).Milliseconds()
		// log.Printf(
		// 	"access server=%s method=%s path=%s raw_query=%q status=%d latency_ms=%d remote=%s ua=%q",
		// 	serverName, r.Method, r.URL.Path, r.URL.RawQuery, rec.status, latency, r.RemoteAddr, r.UserAgent(),
		// )
	})
}
