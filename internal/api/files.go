package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"runova/internal/apps"
	"runova/internal/auth"
)

// fileRoutes serve the app's workspace: list, download, upload and delete.
func (s *server) fileRoutes(mux *http.ServeMux, user func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/apps/{id}/files", user(s.listFiles))
	mux.HandleFunc("GET /api/apps/{id}/files/download", user(s.downloadFile))
	mux.HandleFunc("POST /api/apps/{id}/files", user(s.uploadFile))
	mux.HandleFunc("DELETE /api/apps/{id}/files", user(s.deleteFile))
}

func (s *server) listFiles(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	entries, err := s.apps.ListFiles(r.Context(), u, id, rel)
	if err != nil {
		appError(w, r, err)
		return
	}
	app, _, err := s.apps.Get(r.Context(), u, id)
	if err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		// Root is how the panel names the workspace: the app's name, never a host path.
		Root    string           `json:"root"`
		Path    string           `json:"path"`
		Entries []apps.FileEntry `json:"entries"`
	}{app.Name, rel, entries})
}

func (s *server) downloadFile(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	f, fi, err := s.apps.OpenFile(r.Context(), u, id, r.URL.Query().Get("path"))
	if err != nil {
		appError(w, r, err)
		return
	}
	defer f.Close()
	// Always a download, never rendered: an uploaded HTML file must not run in the panel's origin.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fi.Name()}))
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *server) uploadFile(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.apps.Get(r.Context(), u, id); err != nil {
		appError(w, r, err)
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	limit := int64(settings.UploadLimitMB) << 20
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "multipart/form-data" {
		writeError(w, http.StatusUnsupportedMediaType, "Unggahan harus berupa multipart/form-data")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+multipartSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Unggahan tidak valid")
		return
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			writeError(w, http.StatusBadRequest, "Field \"file\" wajib berisi file yang diunggah")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "Unggahan tidak valid")
			return
		}
		if part.FormName() != uploadFieldName || part.FileName() == "" {
			continue
		}
		err = s.apps.SaveFile(r.Context(), u, id, r.URL.Query().Get("path"), part.FileName(), part)
		if _, big := errors.AsType[*http.MaxBytesError](err); big {
			writeError(w, http.StatusRequestEntityTooLarge, "File melebihi batas "+strconv.Itoa(settings.UploadLimitMB)+" MB")
			return
		}
		if err != nil {
			appError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, okBody)
		return
	}
}

func (s *server) deleteFile(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var in struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.apps.DeleteFile(r.Context(), u, id, in.Path); err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody)
}
