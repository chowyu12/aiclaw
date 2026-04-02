package handler

import (
	"net/http"

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
}

type skillItem struct {
	DirName     string           `json:"dir_name"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Version     string           `json:"version"`
	Author      string           `json:"author"`
	Slug        string           `json:"slug"`
	MainFile    string           `json:"main_file"`
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
