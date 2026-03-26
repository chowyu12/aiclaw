package handler

import (
	"net/http"

	"github.com/chowyu12/aiclaw/internal/skills"
	"github.com/chowyu12/aiclaw/internal/workspace"
	"github.com/chowyu12/aiclaw/pkg/httputil"
)

type SkillsHandler struct{}

func NewSkillsHandler() *SkillsHandler {
	return &SkillsHandler{}
}

func (h *SkillsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/workspace/skills", h.List)
}

type workspaceSkillItem struct {
	DirName     string `json:"dir_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Slug        string `json:"slug"`
	MainFile    string `json:"main_file"`
}

func (h *SkillsHandler) List(w http.ResponseWriter, r *http.Request) {
	skillsDir := workspace.Skills()
	if skillsDir == "" {
		httputil.InternalError(w, "workspace not initialized")
		return
	}
	infos, err := skills.ScanAll(skillsDir)
	if err != nil {
		httputil.InternalError(w, err.Error())
		return
	}
	out := make([]workspaceSkillItem, 0, len(infos))
	for _, info := range infos {
		out = append(out, workspaceSkillItem{
			DirName:     info.DirName,
			Name:        info.Name,
			Description: info.Description,
			Version:     info.Version,
			Author:      info.Author,
			Slug:        info.Slug,
			MainFile:    info.MainFile,
		})
	}
	httputil.OK(w, map[string]any{"list": out})
}
