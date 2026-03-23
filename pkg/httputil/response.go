package httputil

import (
	"encoding/json"
	"net/http"
)

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitzero"`
}

type PageData struct {
	List  any   `json:"list"`
	Total int64 `json:"total"`
}

func JSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func OK(w http.ResponseWriter, data any) {
	JSON(w, http.StatusOK, Response{Code: 0, Message: "ok", Data: data})
}

func OKList(w http.ResponseWriter, list any, total int64) {
	JSON(w, http.StatusOK, Response{Code: 0, Message: "ok", Data: PageData{List: list, Total: total}})
}

func Error(w http.ResponseWriter, httpCode int, msg string) {
	JSON(w, httpCode, Response{Code: -1, Message: msg})
}

func BadRequest(w http.ResponseWriter, msg string) {
	Error(w, http.StatusBadRequest, msg)
}

func NotFound(w http.ResponseWriter, msg string) {
	Error(w, http.StatusNotFound, msg)
}

func Unauthorized(w http.ResponseWriter, msg string) {
	Error(w, http.StatusUnauthorized, msg)
}

func Forbidden(w http.ResponseWriter, msg string) {
	Error(w, http.StatusForbidden, msg)
}

func InternalError(w http.ResponseWriter, msg string) {
	Error(w, http.StatusInternalServerError, msg)
}

func BindJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
