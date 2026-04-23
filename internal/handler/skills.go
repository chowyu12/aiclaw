package handler

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	agentpkg "github.com/chowyu12/aiclaw/internal/agent"
	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/skills"
	"github.com/chowyu12/aiclaw/internal/workspace"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type SkillsHandler struct {
	ws *workspace.Workspace
}

func NewSkillsHandler(ws *workspace.Workspace) *SkillsHandler {
	return &SkillsHandler{ws: ws}
}

func (h *SkillsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/workspace/skills", h.List)
	mux.HandleFunc("GET /api/v1/workspace/skills/pending", h.ListPending)
	mux.HandleFunc("GET /api/v1/workspace/skills/pending/{file}", h.ReadPending)
	mux.HandleFunc("POST /api/v1/workspace/skills/pending/{file}/promote", h.PromotePending)
	mux.HandleFunc("DELETE /api/v1/workspace/skills/pending/{file}", h.DiscardPending)
}

type skillItem struct {
	DirName     string            `json:"dir_name"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Author      string            `json:"author"`
	Slug        string            `json:"slug"`
	MainFile    string            `json:"main_file"`
	Source      model.SkillSource `json:"source"`
}

func (h *SkillsHandler) List(w http.ResponseWriter, r *http.Request) {
	builtinSkills := skills.BuiltinSkills()
	builtinItems := make([]skillItem, 0, len(builtinSkills))
	for _, s := range builtinSkills {
		builtinItems = append(builtinItems, skillItem{
			DirName:     s.DirName,
			Name:        s.Name,
			Description: s.Description,
			Version:     s.Version,
			Author:      s.Author,
			Source:      model.SkillSourceBuiltin,
		})
	}

	builtinDirs := make(map[string]bool, len(builtinSkills))
	for _, s := range builtinSkills {
		builtinDirs[s.DirName] = true
	}

	var localItems []skillItem
	skillsDir := h.ws.Skills()
	if skillsDir != "" {
		infos, err := skills.ScanAll(skillsDir)
		if err != nil {
			httputil.InternalError(w, err.Error())
			return
		}
		localItems = make([]skillItem, 0, len(infos))
		for _, info := range infos {
			if builtinDirs[info.DirName] {
				continue
			}
			localItems = append(localItems, skillItem{
				DirName:     info.DirName,
				Name:        info.Name,
				Description: info.Description,
				Version:     info.Version,
				Author:      info.Author,
				Slug:        info.Slug,
				MainFile:    info.MainFile,
				Source:      model.SkillSourceLocal,
			})
		}
	}

	httputil.OK(w, map[string]any{
		"builtin": builtinItems,
		"local":   localItems,
	})
}

// ─────────── Pending skill candidates (auto-crystallized) ───────────

type pendingSkillItem struct {
	FileName  string    `json:"file_name"`
	UpdatedAt time.Time `json:"updated_at"`
	Preview   string    `json:"preview"`
}

// ListPending 返回 skills-pending 目录中的候选清单。
func (h *SkillsHandler) ListPending(w http.ResponseWriter, _ *http.Request) {
	items, err := agentpkg.ListPendingSkills(h.ws.Root(), 0)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	out := make([]pendingSkillItem, 0, len(items))
	for _, it := range items {
		out = append(out, pendingSkillItem{
			FileName:  it.FileName,
			UpdatedAt: it.UpdatedAt,
			Preview:   it.Preview,
		})
	}
	httputil.OK(w, map[string]any{"list": out})
}

// ReadPending 返回单个候选的完整内容。
func (h *SkillsHandler) ReadPending(w http.ResponseWriter, r *http.Request) {
	file := strings.TrimSpace(r.PathValue("file"))
	content, err := agentpkg.ReadPendingSkill(h.ws.Root(), file)
	if err != nil {
		writePendingErr(w, err)
		return
	}
	httputil.OK(w, map[string]any{
		"file_name": file,
		"content":   content,
	})
}

type promotePendingReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// PromotePending 将候选转正到 skills/<slug>/SKILL.md。
func (h *SkillsHandler) PromotePending(w http.ResponseWriter, r *http.Request) {
	file := strings.TrimSpace(r.PathValue("file"))
	var req promotePendingReq
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.BadRequest(w, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	desc := strings.TrimSpace(req.Description)
	if name == "" {
		httputil.BadRequest(w, "name is required")
		return
	}
	if desc == "" {
		httputil.BadRequest(w, "description is required")
		return
	}
	skillDir, err := agentpkg.PromotePendingSkill(h.ws.Root(), h.ws.Skills(), file, name, desc)
	if err != nil {
		writePendingErr(w, err)
		return
	}
	httputil.OK(w, map[string]any{
		"file_name": file,
		"skill_dir": skillDir,
	})
}

// DiscardPending 删除一个候选。
func (h *SkillsHandler) DiscardPending(w http.ResponseWriter, r *http.Request) {
	file := strings.TrimSpace(r.PathValue("file"))
	if err := agentpkg.DiscardPendingSkill(h.ws.Root(), file); err != nil {
		writePendingErr(w, err)
		return
	}
	httputil.OK(w, map[string]any{"file_name": file})
}

func writePendingErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		httputil.NotFound(w, err.Error())
	case strings.Contains(err.Error(), "invalid file name"),
		strings.Contains(err.Error(), "is required"),
		strings.Contains(err.Error(), "yields empty slug"):
		httputil.BadRequest(w, err.Error())
	default:
		httputil.InternalError(w, err.Error())
	}
}
