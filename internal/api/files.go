package api

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"runova/internal/apps"
	"runova/internal/auth"
	"strconv"
)

// fileRoutes serve the app's workspace: list, download, preview, upload, create folders, move and delete.
func (s *server) fileRoutes(mux *http.ServeMux, user func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/apps/{id}/files", user(s.listFiles))
	mux.HandleFunc("GET /api/apps/{id}/files/download", user(s.downloadFile))
	mux.HandleFunc("GET /api/apps/{id}/files/zip", user(s.downloadZip))
	mux.HandleFunc("GET /api/apps/{id}/files/info", user(s.fileInfo))
	mux.HandleFunc("GET /api/apps/{id}/files/preview", user(s.previewFile))
	mux.HandleFunc("POST /api/apps/{id}/files", user(s.uploadFile))
	mux.HandleFunc("POST /api/apps/{id}/files/folder", user(s.makeFolder))
	mux.HandleFunc("POST /api/apps/{id}/files/move", user(s.moveFile))
	mux.HandleFunc("DELETE /api/apps/{id}/files", user(s.deleteFiles))
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
	saved := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			if saved == 0 {
				writeError(w, http.StatusBadRequest, "Field \"file\" wajib berisi file yang diunggah")
				return
			}
			writeJSON(w, http.StatusCreated, struct {
				Saved int `json:"saved"`
			}{saved})
			return
		}
		if _, big := errors.AsType[*http.MaxBytesError](err); big {
			writeError(w, http.StatusRequestEntityTooLarge, "Unggahan melebihi batas "+strconv.Itoa(settings.UploadLimitMB)+" MB")
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
			writeError(w, http.StatusRequestEntityTooLarge, "Unggahan melebihi batas "+strconv.Itoa(settings.UploadLimitMB)+" MB")
			return
		}
		if err != nil {
			appError(w, r, err)
			return
		}
		saved++
	}
}

func (s *server) deleteFiles(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var in struct {
		Paths []string `json:"paths"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.apps.DeleteFiles(r.Context(), u, id, in.Paths); err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody)
}

func (s *server) makeFolder(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var in struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.apps.MakeDir(r.Context(), u, id, in.Path, in.Name); err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, okBody)
}

func (s *server) moveFile(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.apps.MoveFile(r.Context(), u, id, in.From, in.To); err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody)
}

func (s *server) fileInfo(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	d, err := s.apps.FileInfo(r.Context(), u, id, r.URL.Query().Get("path"))
	if err != nil {
		appError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// previewFile serves a whitelisted image or a small text file inline. The
// type is the sniffed one (text always as text/plain), the response is
// sandboxed and never sniffed again, so an uploaded HTML or SVG file cannot
// run in the panel's origin.
func (s *server) previewFile(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	f, fi, d, err := s.apps.OpenPreview(r.Context(), u, id, r.URL.Query().Get("path"))
	if err != nil {
		appError(w, r, err)
		return
	}
	defer f.Close()
	ct := d.MIME
	if d.Preview == "text" {
		ct = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": fi.Name()}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// downloadZip streams the selected files (one folder's items, or the whole
// workspace with path="") as a ZIP. The selection is measured first and
// refused above 4× the upload limit (at most 2 GB).
func (s *server) downloadZip(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	id, ok := appID(w, r)
	if !ok {
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	plan, err := s.apps.PrepareZip(r.Context(), u, id, r.URL.Query()["path"], zipLimit(settings.UploadLimitMB))
	if err != nil {
		appError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": plan.Name}))
	w.Header().Set("Cache-Control", "no-store")
	if err := plan.Write(w); err != nil {
		// Headers are gone; the client sees a truncated archive.
		slog.Warn("zip download", "app", id, "err", err)
	}
}

// zipLimit is the largest ZIP download: four uploads, at most 2 GB.
func zipLimit(uploadMB int) int64 {
	return min(int64(uploadMB)<<22, 2<<30)
}
