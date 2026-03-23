package handler

import (
	"net/http"
	"strconv"

	"github.com/chowyu12/aiclaw/internal/model"
)

// ParseListQuery 解析通用分页/关键词查询参数。
func ParseListQuery(r *http.Request) model.ListQuery {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	return model.ListQuery{
		Page:     page,
		PageSize: pageSize,
		Keyword:  r.URL.Query().Get("keyword"),
	}
}
