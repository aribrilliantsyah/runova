package apps

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"

	"runova/internal/store"
)

const (
	// PreviewTextMax is the largest text file the panel shows inline.
	PreviewTextMax = 1 << 20
	sniffLen       = 512
)

// previewImages are the image types the panel renders inline. SVG is missing
// on purpose: it can carry script, so it is only ever downloaded.
var previewImages = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// FileDetail describes one regular file for the preview panel. MIME is
// sniffed from the content, never taken from the name. Preview is "image",
// "text" or "" (download only).
type FileDetail struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
	MIME    string `json:"mime"`
	Preview string `json:"preview"`
}

// sniff reads the start of f and names its type the way browsers would,
// telling SVG apart from other XML so it is never treated as plain text.
func sniff(f io.ReaderAt) string {
	buf := make([]byte, sniffLen)
	n, _ := f.ReadAt(buf, 0)
	buf = buf[:n]
	mime := http.DetectContentType(buf)
	if strings.HasPrefix(mime, "text/") && bytes.Contains(bytes.ToLower(buf), []byte("<svg")) {
		return "image/svg+xml"
	}
	return mime
}

func previewKind(mime string, size int64) string {
	switch {
	case slices.Contains(previewImages, mime):
		return "image"
	case strings.HasPrefix(mime, "text/") && size <= PreviewTextMax:
		return "text"
	}
	return ""
}

// FileInfo describes one regular file of the workspace for the preview panel.
func (s *Service) FileInfo(ctx context.Context, actor store.User, appID int64, rel string) (FileDetail, error) {
	f, fi, err := s.OpenFile(ctx, actor, appID, rel)
	if err != nil {
		return FileDetail{}, err
	}
	defer f.Close()
	rel, _ = cleanRel(rel)
	mime := sniff(f)
	return FileDetail{Name: fi.Name(), Path: rel, Size: fi.Size(), ModTime: fi.ModTime().UnixMilli(), MIME: mime, Preview: previewKind(mime, fi.Size())}, nil
}

// OpenPreview opens a file the panel may show inline: an image of a
// whitelisted type or text up to PreviewTextMax. The returned type is the
// sniffed one; anything else is refused and has to be downloaded.
func (s *Service) OpenPreview(ctx context.Context, actor store.User, appID int64, rel string) (*os.File, fs.FileInfo, FileDetail, error) {
	f, fi, err := s.OpenFile(ctx, actor, appID, rel)
	if err != nil {
		return nil, nil, FileDetail{}, err
	}
	mime := sniff(f)
	d := FileDetail{Name: fi.Name(), Size: fi.Size(), ModTime: fi.ModTime().UnixMilli(), MIME: mime, Preview: previewKind(mime, fi.Size())}
	if d.Preview == "" {
		f.Close()
		return nil, nil, FileDetail{}, invalid("%s tidak bisa dipratinjau; unduh filenya", fi.Name())
	}
	return f, fi, d, nil
}

// ZipPlan is a checked selection of workspace files ready to be streamed as a
// ZIP. Size is the total of its regular files when it was prepared.
type ZipPlan struct {
	Name  string
	Size  int64
	limit int64
	svc   *Service
	owner store.User
	app   store.App
	items []zipItem
}

// zipItem is one selected path and the name it has inside the archive.
type zipItem struct {
	rel, name string
}

// PrepareZip checks a selection from one folder (or "" for the whole
// workspace) and adds up its size. Links are left out, never followed; a
// selection larger than limit bytes is refused before anything is sent.
func (s *Service) PrepareZip(ctx context.Context, actor store.User, appID int64, paths []string, limit int64) (*ZipPlan, error) {
	app, owner, err := s.load(ctx, actor, appID)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, invalid("Pilih file yang akan diunduh")
	}
	plan := &ZipPlan{limit: limit, svc: s, owner: owner, app: app}
	parent := ""
	for i, p := range paths {
		rel, err := cleanRel(p)
		if err != nil {
			return nil, err
		}
		name := path.Base(rel)
		if rel == "" {
			if len(paths) > 1 {
				return nil, invalid("Workspace utuh diunduh sendiri, tanpa pilihan lain")
			}
			name = app.Name
		}
		dir := path.Dir(rel)
		if dir == "." {
			dir = ""
		}
		if i == 0 {
			parent = dir
		} else if dir != parent {
			return nil, invalid("Pilih file dari satu folder yang sama")
		}
		if slices.ContainsFunc(plan.items, func(it zipItem) bool { return it.rel == rel }) {
			return nil, invalid("%s dipilih dua kali", rel)
		}
		if err := s.lstatInside(owner, app, rel); err != nil {
			return nil, err
		}
		plan.items = append(plan.items, zipItem{rel, name})
	}
	plan.Name = plan.items[0].name + ".zip"
	if len(plan.items) > 1 {
		plan.Name = app.Name + ".zip"
	}
	for _, it := range plan.items {
		err := s.walkWorkspace(ctx, owner, app, it.rel, it.name, func(_, _ string, fi fs.FileInfo) error {
			if fi.Mode().IsRegular() {
				plan.Size += fi.Size()
				if plan.Size > limit {
					return invalid("Unduhan melebihi batas %d MB; pilih lebih sedikit file", limit>>20)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// lstatInside checks rel exists in the workspace without following a link on the way.
func (s *Service) lstatInside(owner store.User, app store.App, rel string) error {
	root, _, name, err := s.workspaceRoot(owner, app, rel)
	if err != nil {
		return fileErr(err)
	}
	defer root.Close()
	_, err = root.Lstat(name)
	return fileErr(err)
}

// walkWorkspace calls fn for rel and everything under it with its archive
// name. Links are skipped (never followed); a mounted saved folder is walked
// in its own host folder, as the File tab shows it.
func (s *Service) walkWorkspace(ctx context.Context, owner store.User, app store.App, rel, name string, fn func(rel, name string, fi fs.FileInfo) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, _, inner, err := s.workspaceRoot(owner, app, rel)
	if err != nil {
		return fileErr(err)
	}
	fi, err := root.Lstat(inner)
	if err != nil {
		root.Close()
		return fileErr(err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 || (!fi.IsDir() && !fi.Mode().IsRegular()) {
		root.Close()
		return nil
	}
	if err := fn(rel, name, fi); err != nil || !fi.IsDir() {
		root.Close()
		return err
	}
	d, err := root.Open(inner)
	if err != nil {
		root.Close()
		return fileErr(err)
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	root.Close()
	if err != nil {
		return err
	}
	slices.Sort(names)
	for _, n := range names {
		if err := s.walkWorkspace(ctx, owner, app, joinRel(rel, n), name+"/"+n, fn); err != nil {
			return err
		}
	}
	return nil
}

// Write streams the archive to w. Files are copied up to their size at the
// time they are read and the total stays within the plan's limit, so a file
// growing during the download cannot make it unbounded.
func (p *ZipPlan) Write(w io.Writer) error {
	zw := zip.NewWriter(w)
	var total int64
	ctx := context.Background()
	for _, it := range p.items {
		err := p.svc.walkWorkspace(ctx, p.owner, p.app, it.rel, it.name, func(rel, name string, fi fs.FileInfo) error {
			h := &zip.FileHeader{Name: name, Modified: fi.ModTime(), Method: zip.Deflate}
			if fi.IsDir() {
				h.Name += "/"
				h.Method = zip.Store
				_, err := zw.CreateHeader(h)
				return err
			}
			if total += fi.Size(); total > p.limit {
				return fmt.Errorf("zip grew past %d bytes", p.limit)
			}
			f, _, err := p.svc.openRegular(p.owner, p.app, rel)
			if err != nil {
				return err
			}
			defer f.Close()
			fw, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(fw, f, fi.Size()); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return zw.Close()
}
