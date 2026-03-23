package server

import (
	"io/fs"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/web"
)

// MountEmbeddedFrontend 挂载打包进二进制的 Vue 静态资源（GET / 及静态文件）。
func MountEmbeddedFrontend(mux *http.ServeMux) {
	distFS, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		log.WithError(err).Warn("embedded frontend not available, skipping")
		return
	}
	fileServer := http.FileServer(http.FS(distFS))
	log.Info("serving embedded frontend")

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(distFS, path[1:]); err != nil {
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}
