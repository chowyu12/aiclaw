package handler

import (
	"net/http"
	"strconv"

	"github.com/chowyu12/aiclaw/internal/scheduler"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type SchedulerHandler struct {
	sched *scheduler.Scheduler
}

func NewSchedulerHandler(s *scheduler.Scheduler) *SchedulerHandler {
	return &SchedulerHandler{sched: s}
}

func (h *SchedulerHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/scheduler/jobs", h.ListJobs)
	mux.HandleFunc("GET /api/v1/scheduler/jobs/{id}/logs", h.GetLogs)
	mux.HandleFunc("PUT /api/v1/scheduler/jobs/{id}/toggle", h.ToggleJob)
	mux.HandleFunc("DELETE /api/v1/scheduler/jobs/{id}", h.DeleteJob)
}

func (h *SchedulerHandler) ListJobs(w http.ResponseWriter, r *http.Request) {
	if h.sched == nil {
		httputil.OK(w, []any{})
		return
	}
	jobs := h.sched.ListJobs()
	if jobs == nil {
		jobs = []*scheduler.Job{}
	}
	httputil.OK(w, jobs)
}

func (h *SchedulerHandler) GetLogs(w http.ResponseWriter, r *http.Request) {
	if h.sched == nil {
		httputil.OK(w, []any{})
		return
	}
	jobID := r.PathValue("id")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
		limit = n
	}
	logs := h.sched.ListLogs(jobID, limit)
	if logs == nil {
		logs = []scheduler.RunRecord{}
	}
	httputil.OK(w, logs)
}

func (h *SchedulerHandler) ToggleJob(w http.ResponseWriter, r *http.Request) {
	if h.sched == nil {
		httputil.InternalError(w, "scheduler not available")
		return
	}
	jobID := r.PathValue("id")

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := httputil.BindJSON(r, &body); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}

	if err := h.sched.ToggleJob(r.Context(), jobID, body.Enabled); err != nil {
		httputil.NotFound(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}

func (h *SchedulerHandler) DeleteJob(w http.ResponseWriter, r *http.Request) {
	if h.sched == nil {
		httputil.InternalError(w, "scheduler not available")
		return
	}
	jobID := r.PathValue("id")
	if err := h.sched.RemoveJob(jobID); err != nil {
		httputil.NotFound(w, err.Error())
		return
	}
	httputil.OK(w, nil)
}
