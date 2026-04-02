package handler

import (
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/chowyu12/aiclaw/pkg/httputil"
)

// Metrics 收集基础运行时指标（无需外部依赖）。
var Metrics = &metrics{startTime: time.Now()}

type metrics struct {
	startTime    time.Time
	apiRequests  atomic.Int64
	apiErrors    atomic.Int64
	chatRequests atomic.Int64
}

func (m *metrics) IncAPI()  { m.apiRequests.Add(1) }
func (m *metrics) IncErr()  { m.apiErrors.Add(1) }
func (m *metrics) IncChat() { m.chatRequests.Add(1) }

func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Metrics.IncAPI()
		next.ServeHTTP(w, r)
	})
}

type MetricsHandler struct{}

func NewMetricsHandler() *MetricsHandler { return &MetricsHandler{} }

func (h *MetricsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/metrics", h.Get)
}

func (h *MetricsHandler) Get(w http.ResponseWriter, _ *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	httputil.OK(w, map[string]any{
		"uptime_seconds": int(time.Since(Metrics.startTime).Seconds()),
		"goroutines":     runtime.NumGoroutine(),
		"memory_alloc":   mem.Alloc,
		"memory_sys":     mem.Sys,
		"gc_cycles":      mem.NumGC,
		"api_requests":   Metrics.apiRequests.Load(),
		"api_errors":     Metrics.apiErrors.Load(),
		"chat_requests":  Metrics.chatRequests.Load(),
	})
}
