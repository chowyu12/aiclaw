package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/workspace"
)

// JobType 任务触发后执行的类型。
type JobType string

const (
	JobTypePrompt  JobType = "prompt"
	JobTypeCommand JobType = "command"
)

// Job 持久化的定时任务定义。
type Job struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Expression  string    `json:"expression"`
	Type        JobType   `json:"type"`
	AgentUUID   string    `json:"agent_uuid,omitempty"`
	Prompt      string    `json:"prompt,omitempty"`
	Command     string    `json:"command,omitempty"`
	UserID      string    `json:"user_id,omitempty"`
	Enabled     bool      `json:"enabled"`
	MaxRuns     int       `json:"max_runs,omitempty"`
	RunCount    int       `json:"run_count"`
	CreatedAt   time.Time `json:"created_at"`
	LastRunAt   time.Time `json:"last_run_at,omitzero"`
	NextRunAt   time.Time `json:"next_run_at,omitzero"`
	Description string    `json:"description,omitempty"`
}

// RunRecord 单次执行记录。
type RunRecord struct {
	JobID     string    `json:"job_id"`
	RunAt     time.Time `json:"run_at"`
	Duration  string    `json:"duration"`
	Status    string    `json:"status"`
	Output    string    `json:"output,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// JobExecutor 执行任务的回调接口。
type JobExecutor func(ctx context.Context, job *Job) (output string, err error)

// Scheduler 内置定时任务调度器。
type Scheduler struct {
	mu       sync.RWMutex
	cron     *cron.Cron
	jobs     map[string]*Job
	entryIDs map[string]cron.EntryID
	executor JobExecutor
	ws       *workspace.Workspace
	dataFile string
	logDir   string
	cancel   context.CancelFunc
}

// New 创建调度器实例。
func New(ws *workspace.Workspace, exec JobExecutor) *Scheduler {
	dataDir := filepath.Join(ws.Root(), "scheduler")
	os.MkdirAll(dataDir, 0o755)

	return &Scheduler{
		cron:     cron.New(cron.WithSeconds()),
		jobs:     make(map[string]*Job),
		entryIDs: make(map[string]cron.EntryID),
		executor: exec,
		ws:       ws,
		dataFile: filepath.Join(dataDir, "jobs.json"),
		logDir:   filepath.Join(dataDir, "logs"),
	}
}

// Start 加载持久化任务并启动调度。
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	os.MkdirAll(s.logDir, 0o755)

	s.loadJobs()
	s.mu.RLock()
	for _, job := range s.jobs {
		if job.Enabled {
			s.scheduleJobLocked(ctx, job)
		}
	}
	s.mu.RUnlock()

	s.cron.Start()
	log.WithField("jobs", len(s.jobs)).Info("[Scheduler] started")
}

// Stop 停止调度器。
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	ctx := s.cron.Stop()
	<-ctx.Done()
	log.Info("[Scheduler] stopped")
}

// AddJob 添加一个定时任务。
func (s *Scheduler) AddJob(ctx context.Context, job *Job) error {
	if job.ID == "" {
		job.ID = fmt.Sprintf("job_%d", time.Now().UnixMilli())
	}
	if job.Expression == "" {
		return fmt.Errorf("expression is required")
	}
	job.CreatedAt = time.Now()
	job.Enabled = true

	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sched, err := parser.Parse(job.Expression)
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", job.Expression, err)
	}
	job.NextRunAt = sched.Next(time.Now())

	s.mu.Lock()
	defer s.mu.Unlock()

	s.jobs[job.ID] = job
	s.scheduleJobLocked(ctx, job)
	s.saveJobsLocked()
	return nil
}

// RemoveJob 删除一个定时任务。
func (s *Scheduler) RemoveJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.jobs[id]; !ok {
		return fmt.Errorf("job %q not found", id)
	}

	if entryID, ok := s.entryIDs[id]; ok {
		s.cron.Remove(entryID)
		delete(s.entryIDs, id)
	}
	delete(s.jobs, id)
	s.saveJobsLocked()
	return nil
}

// ToggleJob 启用/禁用任务。
func (s *Scheduler) ToggleJob(ctx context.Context, id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}

	job.Enabled = enabled
	if enabled {
		s.scheduleJobLocked(ctx, job)
	} else {
		if entryID, eOk := s.entryIDs[id]; eOk {
			s.cron.Remove(entryID)
			delete(s.entryIDs, id)
		}
	}
	s.saveJobsLocked()
	return nil
}

// ListJobs 返回所有任务的副本。
func (s *Scheduler) ListJobs() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		cp := *j
		if eid, ok := s.entryIDs[j.ID]; ok {
			entry := s.cron.Entry(eid)
			if !entry.Next.IsZero() {
				cp.NextRunAt = entry.Next
			}
		}
		result = append(result, &cp)
	}
	return result
}

// GetJob 返回单个任务。
func (s *Scheduler) GetJob(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

// ListLogs 返回指定任务的最近执行日志。
func (s *Scheduler) ListLogs(jobID string, limit int) []RunRecord {
	logPath := filepath.Join(s.logDir, jobID+".jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}

	var records []RunRecord
	for _, line := range splitLines(string(data)) {
		var r RunRecord
		if json.Unmarshal([]byte(line), &r) == nil {
			records = append(records, r)
		}
	}

	if limit > 0 && len(records) > limit {
		records = records[len(records)-limit:]
	}
	return records
}

func (s *Scheduler) scheduleJobLocked(ctx context.Context, job *Job) {
	if old, ok := s.entryIDs[job.ID]; ok {
		s.cron.Remove(old)
		delete(s.entryIDs, job.ID)
	}

	jobCopy := *job
	entryID, err := s.cron.AddFunc(job.Expression, func() {
		s.executeJob(ctx, &jobCopy)
	})
	if err != nil {
		log.WithError(err).WithField("job_id", job.ID).Error("[Scheduler] schedule failed")
		return
	}
	s.entryIDs[job.ID] = entryID
}

func (s *Scheduler) executeJob(ctx context.Context, job *Job) {
	l := log.WithFields(log.Fields{"job_id": job.ID, "job_name": job.Name})
	l.Info("[Scheduler] executing job")

	start := time.Now()
	output, err := s.executor(ctx, job)
	dur := time.Since(start)

	record := RunRecord{
		JobID:    job.ID,
		RunAt:    start,
		Duration: dur.String(),
		Status:   "success",
		Output:   truncateOutput(output, 2000),
	}
	if err != nil {
		record.Status = "error"
		record.Error = err.Error()
		l.WithError(err).WithField("duration", dur).Error("[Scheduler] job failed")
	} else {
		l.WithField("duration", dur).Info("[Scheduler] job completed")
	}

	s.appendLog(record)

	s.mu.Lock()
	if j, ok := s.jobs[job.ID]; ok {
		j.RunCount++
		j.LastRunAt = start
		if j.MaxRuns > 0 && j.RunCount >= j.MaxRuns {
			j.Enabled = false
			if eid, eok := s.entryIDs[job.ID]; eok {
				s.cron.Remove(eid)
				delete(s.entryIDs, job.ID)
			}
			l.WithField("max_runs", j.MaxRuns).Info("[Scheduler] job disabled (max runs reached)")
		}
		s.saveJobsLocked()
	}
	s.mu.Unlock()
}

func (s *Scheduler) appendLog(record RunRecord) {
	data, _ := json.Marshal(record)
	logPath := filepath.Join(s.logDir, record.JobID+".jsonl")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(string(data) + "\n")
}

func (s *Scheduler) loadJobs() {
	data, err := os.ReadFile(s.dataFile)
	if err != nil {
		return
	}
	var jobs []*Job
	if json.Unmarshal(data, &jobs) == nil {
		for _, j := range jobs {
			s.jobs[j.ID] = j
		}
	}
	log.WithField("count", len(s.jobs)).Debug("[Scheduler] jobs loaded")
}

func (s *Scheduler) saveJobsLocked() {
	jobs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	data, _ := json.MarshalIndent(jobs, "", "  ")
	os.MkdirAll(filepath.Dir(s.dataFile), 0o755)
	os.WriteFile(s.dataFile, data, 0o644)
}

func splitLines(s string) []string {
	var lines []string
	for _, line := range splitByNewline(s) {
		line = trimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func splitByNewline(s string) []string {
	var result []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r' || s[j-1] == '\n') {
		j--
	}
	return s[i:j]
}

func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}
