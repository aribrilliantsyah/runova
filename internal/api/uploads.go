package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"runova/internal/artifact"
	"runova/internal/auth"
	"runova/internal/store"
)

const (
	uploadTTL        = 24 * time.Hour
	multipartSlack   = 1 << 20
	uploadFieldName  = "file"
	uploadFileName   = "upload"
	uploadFilesDir   = "files"
	maxClientNameLen = 255
)

type uploadResponse struct {
	ID          string          `json:"id"`
	Report      artifact.Report `json:"report"`
	AllowedJava []int           `json:"allowed_java"`
}

// uploadsDir is where in-flight and unclaimed uploads live.
func uploadsDir(dataDir string) string { return filepath.Join(dataDir, "tmp") }

// upload streams the multipart "file" field to $RUNOVA_DATA/tmp/<id>/, inspects
// it and stores a report. Nothing uploaded is ever executed. Any failure
// removes the upload directory.
func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	user := auth.FromContext(r.Context())
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	limit := int64(settings.UploadLimitMB) << 20
	tooLarge := func() {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("File melebihi batas %d MB", settings.UploadLimitMB))
	}
	pending, err := s.store.CountUploads(r.Context(), user.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if pending >= settings.MaxPendingUploads {
		writeError(w, http.StatusConflict, fmt.Sprintf("Hapus unggahan lama dulu (maksimal %d)", settings.MaxPendingUploads))
		return
	}

	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "multipart/form-data" {
		writeError(w, http.StatusUnsupportedMediaType, "Unggahan harus berupa multipart/form-data")
		return
	}
	if r.ContentLength > limit+multipartSlack {
		tooLarge()
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+multipartSlack)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Unggahan tidak valid")
		return
	}

	id := auth.NewToken()[:24]
	dir := filepath.Join(uploadsDir(s.dataDir), id)
	created := false
	defer func() {
		if !created {
			os.RemoveAll(dir)
		}
	}()
	if err := os.MkdirAll(filepath.Join(dir, uploadFilesDir), 0o700); err != nil {
		serverError(w, r, err)
		return
	}

	clientName, err := s.receiveFile(mr, filepath.Join(dir, uploadFileName), limit)
	switch {
	case errors.Is(err, errNoFile):
		writeError(w, http.StatusBadRequest, "Field \"file\" wajib berisi file yang diunggah")
		return
	case errors.Is(err, artifact.ErrTooLarge):
		tooLarge()
		return
	case err != nil:
		if _, big := errors.AsType[*http.MaxBytesError](err); big {
			tooLarge()
			return
		}
		if r.Context().Err() == nil {
			slog.Warn("upload aborted", "user", user.Username, "err", err)
		}
		writeError(w, http.StatusBadRequest, "Unggahan terputus atau tidak valid")
		return
	}

	rawPath := filepath.Join(dir, uploadFileName)
	report, keep, err := artifact.Inspect(rawPath, clientName, filepath.Join(dir, uploadFilesDir), limit)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if keep != rawPath {
		os.Remove(rawPath)
	}
	allowed := applyRuntimePolicy(&report, settings.Runtimes)

	up := store.Upload{
		ID: id, UserID: user.ID, Dir: dir, ArtifactPath: keep,
		Report: report, CreatedAt: s.now().UnixMilli(),
	}
	if err := s.store.CreateUpload(r.Context(), up); err != nil {
		serverError(w, r, err)
		return
	}
	created = true
	s.record(r, user, store.ActUpload, report.FileName, string(report.Kind))
	writeJSON(w, http.StatusCreated, uploadResponse{ID: id, Report: report, AllowedJava: allowed})
}

var errNoFile = errors.New("no file part")

// receiveFile copies the "file" part to dst, allowing at most limit bytes
// (artifact.ErrTooLarge beyond that). It returns the client's file name.
func (s *server) receiveFile(mr *multipart.Reader, dst string, limit int64) (string, error) {
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return "", errNoFile
		}
		if err != nil {
			return "", err
		}
		if part.FormName() != uploadFieldName || part.FileName() == "" {
			continue
		}
		name := filepath.Base(part.FileName())
		if len(name) > maxClientNameLen {
			name = name[len(name)-maxClientNameLen:]
		}

		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", err
		}
		n, err := io.Copy(out, io.LimitReader(part, limit+1))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		switch {
		case err != nil:
			return "", err
		case n > limit:
			return "", artifact.ErrTooLarge
		}
		return name, nil
	}
}

// applyRuntimePolicy adds a problem when the admin disabled every runtime the
// artifact could use, and returns the Java versions the admin allows that are
// at least the artifact's minimum.
func applyRuntimePolicy(rep *artifact.Report, runtimes map[string]bool) []int {
	allowed := []int{}
	switch rep.Kind {
	case artifact.KindJar:
		for _, id := range store.RuntimeIDs {
			v, ok := strings.CutPrefix(id, "java")
			if !ok || !runtimes[id] {
				continue
			}
			if n, err := strconv.Atoi(v); err == nil && n >= rep.MinJava {
				allowed = append(allowed, n)
			}
		}
		slices.Sort(allowed)
		if len(allowed) == 0 && rep.MinJava <= 25 {
			rep.Problems = append(rep.Problems, fmt.Sprintf("Admin menonaktifkan runtime Java %d atau yang lebih baru di server ini", rep.MinJava))
		}
	case artifact.KindStatic, artifact.KindGlibc:
		if !runtimes[string(rep.Kind)] {
			rep.Problems = append(rep.Problems, fmt.Sprintf("Admin menonaktifkan runtime %s di server ini", rep.Kind))
		}
	}
	return allowed
}

func (s *server) deleteUpload(w http.ResponseWriter, r *http.Request) {
	user := auth.FromContext(r.Context())
	up, err := s.store.Upload(r.Context(), r.PathValue("id"), user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Unggahan tidak ditemukan")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	if err := os.RemoveAll(up.Dir); err != nil {
		serverError(w, r, err)
		return
	}
	if err := s.store.DeleteUpload(r.Context(), up.ID); err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okBody)
}

// CleanupUploads removes uploads older than 24 hours: directory first, then
// the row, so a failed removal is retried on the next run.
func CleanupUploads(ctx context.Context, st *store.Store, now time.Time) error {
	expired, err := st.ExpiredUploads(ctx, now.Add(-uploadTTL).UnixMilli())
	if err != nil {
		return err
	}
	var errs []error
	for _, up := range expired {
		if err := os.RemoveAll(up.Dir); err != nil {
			errs = append(errs, fmt.Errorf("remove upload %s: %w", up.ID, err))
			continue
		}
		if err := st.DeleteUpload(ctx, up.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// SweepUploadDirs removes folders under DataDir/tmp that no upload row owns
// and that are older than 24 hours: what a crash in the middle of an upload
// leaves behind. Younger folders may still be receiving their file.
func SweepUploadDirs(dataDir string, st *store.Store, now time.Time) error {
	entries, err := os.ReadDir(uploadsDir(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	owned, err := st.ExpiredUploads(context.Background(), math.MaxInt64)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(owned))
	for _, up := range owned {
		known[up.ID] = true
	}
	cutoff := now.Add(-uploadTTL)
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || known[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		dir := filepath.Join(uploadsDir(dataDir), e.Name())
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, fmt.Errorf("remove orphan upload dir %s: %w", dir, err))
		}
	}
	return errors.Join(errs...)
}
