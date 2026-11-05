package apps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"runova/internal/folders"
	"runova/internal/store"
)

const (
	// defaultDiskMB is the disk a new app gets when the deploy form does not choose one.
	defaultDiskMB = 256
	minDiskMB     = 64
	diskStepMB    = 64
	// diskWarnPercent is where a workspace counts as nearly full.
	diskWarnPercent = 80
	// scanTimeout bounds the walk of one workspace.
	scanTimeout = time.Minute
	// DiskScanInterval is how often WatchDisk measures every workspace.
	DiskScanInterval = 5 * time.Minute
	maxFileName      = 255
)

// mountsRoot holds the sources of an app's mounted folders. It belongs to
// Runova (0700), not to the app, so a running app cannot swap a source for a
// link that would make Docker bind some other host path.
func (s *Service) mountsRoot(username, app string) string {
	return filepath.Join(s.DataDir, "mounts", username, app)
}

// workspaceDirs are the host folders that make up the app's workspace: the data folder and every mount source.
func (s *Service) workspaceDirs(owner store.User, app store.App) []string {
	dirs := []string{s.appDataDir(owner.Username, app.Name)}
	for _, f := range app.Folders {
		if f.Mount() {
			dirs = append(dirs, filepath.Join(s.mountsRoot(owner.Username, app.Name), f.Dir))
		}
	}
	return dirs
}

// chownIn hands name (inside root, which is the host folder base) to uid
// without following links. A test or development Chown hook gets the path.
func (s *Service) chownIn(root *os.Root, base, name string, uid int) error {
	if s.Chown != nil {
		return s.Chown(filepath.Join(base, name), uid)
	}
	return root.Lchown(name, uid, uid)
}

// ensureWorkspace creates the app's data folder, its saved folders inside it
// and the sources of its mounted folders, all owned by the owner's UID. A
// folder that is a link (or leads through one) out of the workspace fails the
// deploy instead of being followed.
func (s *Service) ensureWorkspace(owner store.User, app store.App, artifactFile string) error {
	if _, err := folders.Check(app.Folders, artifactFile); err != nil {
		return invalid("Folder tersimpan: %s", err.Error())
	}
	if err := s.ensureDataDir(owner, app.Name); err != nil {
		return err
	}
	uid := containerUID(owner)
	data := s.appDataDir(owner.Username, app.Name)
	root, err := os.OpenRoot(data)
	if err != nil {
		return fmt.Errorf("open data dir: %w", err)
	}
	defer root.Close()
	mkdirs := func(r *os.Root, base, rel string) error {
		parts := strings.Split(rel, "/")
		for i := range parts {
			p := strings.Join(parts[:i+1], "/")
			fi, err := r.Lstat(p)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				if err := r.Mkdir(p, dirMode); err != nil {
					return err
				}
				if err := s.chownIn(r, base, p, uid); err != nil {
					return err
				}
			case err != nil:
				return err
			case !fi.IsDir():
				return invalid("Folder tersimpan %s: %s di workspace bukan folder (mungkin link); hapus atau ganti namanya lewat tab File", rel, p)
			}
		}
		return nil
	}
	mounts := s.mountsRoot(owner.Username, app.Name)
	hasMount := slices.ContainsFunc(app.Folders, folders.Folder.Mount)
	var mroot *os.Root
	if hasMount {
		if err := os.MkdirAll(mounts, dirMode); err != nil {
			return fmt.Errorf("create mounts dir: %w", err)
		}
		if mroot, err = os.OpenRoot(mounts); err != nil {
			return fmt.Errorf("open mounts dir: %w", err)
		}
		defer mroot.Close()
	}
	for _, f := range app.Folders {
		// The workspace shows every folder under its Dir; for a mount it is the mount point.
		if err := mkdirs(root, data, f.Dir); err != nil {
			return wrapFolderErr(f, err)
		}
		if f.Mount() {
			if err := mkdirs(mroot, mounts, f.Dir); err != nil {
				return wrapFolderErr(f, err)
			}
		}
	}
	return nil
}

func wrapFolderErr(f folders.Folder, err error) error {
	var ue *userError
	if errors.As(err, &ue) {
		return err
	}
	return invalid("Folder tersimpan %s (%s) tidak bisa disiapkan: %v", f.ContainerPath(), f.Dir, err)
}

// folderBinds are the extra bind mounts of the app's mounted folders: each
// source appears at its path in the app and under /data/<dir> in the workspace.
func (s *Service) folderBinds(owner store.User, app store.App) []string {
	var out []string
	for _, f := range app.Folders {
		if !f.Mount() {
			continue
		}
		src := filepath.Join(s.mountsRoot(owner.Username, app.Name), f.Dir)
		out = append(out, src+":"+f.Path, src+":"+folders.DataMount+"/"+f.Dir)
	}
	return out
}

// checkFolders validates a folder list against the app's newest artifact.
func (s *Service) checkFolders(ctx context.Context, app store.App, list []folders.Folder) ([]folders.Folder, error) {
	file := ""
	if ds, err := s.Store.Deployments(ctx, app.ID); err != nil {
		return nil, err
	} else if len(ds) > 0 {
		file = ds[0].FileName
	}
	out, err := folders.Check(list, file)
	if err != nil {
		return nil, invalid("%s", err.Error())
	}
	return out, nil
}

// validateDisk checks a disk size is within the allowed steps, ignoring quota.
func validateDisk(mb int) error {
	if mb < minDiskMB || mb%diskStepMB != 0 {
		return invalid("Disk minimal %d MB dan kelipatan %d MB", minDiskMB, diskStepMB)
	}
	return nil
}

func checkDisk(q store.Quota, used Usage, mb int) error {
	if left := max(q.DiskMB-used.DiskMB, 0); mb > left {
		return overQuota("Disk melebihi sisa kuota (sisa %d MB)", left)
	}
	return nil
}

// defaultDisk is the size of a new app that did not choose one: 256 MB, or
// what is left of the quota in whole steps when that is less.
func defaultDisk(q store.Quota, used Usage) (int, error) {
	left := max(q.DiskMB-used.DiskMB, 0)
	mb := min(defaultDiskMB, left-left%diskStepMB)
	if mb < minDiskMB {
		return 0, overQuota("Kuota disk habis (sisa %d MB dari %d MB)", left, q.DiskMB)
	}
	return mb, nil
}

// diskLevel grades used bytes against a size in MB; an app without a size is never full.
func diskLevel(used int64, sizeMB int) store.DiskLevel {
	if sizeMB <= 0 {
		return store.DiskOK
	}
	limit := int64(sizeMB) << 20
	switch {
	case used >= limit:
		return store.DiskFull
	case used*100 >= limit*diskWarnPercent:
		return store.DiskWarn
	default:
		return store.DiskOK
	}
}

// measure adds up the sizes of the regular files under dirs. Links are
// counted as themselves and never followed; missing folders count 0.
func measure(ctx context.Context, dirs ...string) (int64, error) {
	var total int64
	n := 0
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if n++; n%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if d.Type().IsRegular() {
				if fi, err := d.Info(); err == nil {
					total += fi.Size()
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// measureApp scans the app's workspace and records the result. A higher
// level than before notifies the owner once; a lower one just clears.
func (s *Service) measureApp(ctx context.Context, owner store.User, app store.App) (int64, store.DiskLevel, error) {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	used, err := measure(ctx, s.workspaceDirs(owner, app)...)
	if err != nil {
		return 0, "", fmt.Errorf("measure %s: %w", app.Name, err)
	}
	level := diskLevel(used, app.DiskMB)
	if err := s.Store.SetAppDiskUsage(ctx, app.ID, used, s.now().UnixMilli(), level); err != nil {
		return 0, "", err
	}
	if levelRank(level) > levelRank(app.DiskLevel) {
		usedMB := used >> 20
		if level == store.DiskFull {
			s.notify(ctx, app, store.LevelError, "Disk "+app.Name+" penuh",
				fmt.Sprintf("Workspace %s memakai %d MB dari %d MB. Aplikasi tetap berjalan, tetapi deploy baru ditolak sampai file dihapus (tab File) atau ukuran disk dinaikkan (tab Lanjutan).", app.Name, usedMB, app.DiskMB))
		} else {
			s.notify(ctx, app, store.LevelWarning, "Disk "+app.Name+" hampir penuh",
				fmt.Sprintf("Workspace %s memakai %d MB dari %d MB (%d%% atau lebih).", app.Name, usedMB, app.DiskMB, diskWarnPercent))
		}
	}
	return used, level, nil
}

func levelRank(l store.DiskLevel) int {
	switch l {
	case store.DiskFull:
		return 2
	case store.DiskWarn:
		return 1
	default:
		return 0
	}
}

// ScanDisk measures the workspace of every app. One app that cannot be
// measured does not stop the others; the errors are returned together.
func (s *Service) ScanDisk(ctx context.Context) error {
	list, err := s.Store.AllApps(ctx)
	if err != nil {
		return err
	}
	owners := map[int64]store.User{}
	var errs []error
	for _, app := range list {
		owner, ok := owners[app.UserID]
		if !ok {
			if owner, err = s.Store.User(ctx, app.UserID); err != nil {
				errs = append(errs, err)
				continue
			}
			owners[app.UserID] = owner
		}
		if _, _, err := s.measureApp(ctx, owner, app); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// WatchDisk runs ScanDisk now and then every DiskScanInterval until ctx ends.
func (s *Service) WatchDisk(ctx context.Context) {
	t := time.NewTicker(DiskScanInterval)
	defer t.Stop()
	for {
		if err := s.ScanDisk(ctx); err != nil && ctx.Err() == nil {
			slog.Error("scan app disk", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refuseFullDisk measures the workspace again (so freed space counts at once)
// and refuses a new deploy when it holds the app's whole disk size.
func (s *Service) refuseFullDisk(ctx context.Context, owner store.User, app store.App) error {
	if app.DiskMB <= 0 {
		return nil
	}
	used, level, err := s.measureApp(ctx, owner, app)
	if err != nil {
		return err
	}
	if level == store.DiskFull {
		return overQuota("Disk aplikasi penuh (%d MB dari %d MB). Hapus file di tab File atau naikkan ukuran disk di tab Lanjutan, lalu deploy lagi.", used>>20, app.DiskMB)
	}
	return nil
}

// FileEntry is one item of a workspace folder. Saved marks a saved folder;
// Items counts what a folder holds (one level, links included).
type FileEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // "dir", "file" or "link"
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
	Saved   bool   `json:"saved"`
	Items   int    `json:"items"`
}

var errOutside = &userError{ErrValidation, "Path keluar dari workspace aplikasi"}

// cleanRel validates a workspace-relative path: "" or "." is the workspace itself.
func cleanRel(rel string) (string, error) {
	if strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return "", errOutside
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", errOutside
		}
	}
	return path.Clean("/" + rel)[1:], nil
}

// workspaceRoot opens the host folder that holds rel and returns the name of
// rel inside it: a mounted folder's files live in its source, everything else
// in the data folder.
func (s *Service) workspaceRoot(owner store.User, app store.App, rel string) (root *os.Root, base, name string, err error) {
	first, rest, _ := strings.Cut(rel, "/")
	base = s.appDataDir(owner.Username, app.Name)
	name = rel
	for _, f := range app.Folders {
		if f.Mount() && f.Dir == first {
			base = filepath.Join(s.mountsRoot(owner.Username, app.Name), f.Dir)
			name = rest
			break
		}
	}
	if name == "" {
		name = "."
	}
	root, err = os.OpenRoot(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", "", &userError{store.ErrNotFound, "Workspace belum dibuat; deploy aplikasi dulu"}
	}
	return root, base, name, err
}

// fileErr turns an os.Root error into one for the user.
func fileErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return &userError{store.ErrNotFound, "File tidak ditemukan"}
	case strings.Contains(err.Error(), "escapes") || strings.Contains(err.Error(), "too many links") || errors.Is(err, ErrValidation):
		return errOutside
	default:
		var ue *userError
		if errors.As(err, &ue) {
			return err
		}
		return err
	}
}

func isSaved(app store.App, rel string) bool {
	return slices.ContainsFunc(app.Folders, func(f folders.Folder) bool { return f.Dir == rel })
}

// holdsSaved reports whether rel is a saved folder or a folder with one inside it.
func holdsSaved(app store.App, rel string) bool {
	return slices.ContainsFunc(app.Folders, func(f folders.Folder) bool { return under(f.Dir, rel) })
}

// under reports whether p is dir or inside it; "" is the whole workspace.
func under(p, dir string) bool {
	return dir == "" || p == dir || strings.HasPrefix(p, dir+"/")
}

// validName checks one file or folder name typed by the user.
func validName(name, what string) error {
	if name == "" || name == "." || name == ".." || len(name) > maxFileName || strings.ContainsAny(name, "/\\\x00") {
		return invalid("Nama %s tidak valid", what)
	}
	return nil
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// savedRefusal explains why rel cannot be removed, renamed or moved, or returns nil.
func savedRefusal(app store.App, rel string) error {
	switch {
	case rel == "":
		return invalid("Workspace aplikasi tidak bisa diubah dari sini")
	case isSaved(app, rel):
		return invalid("%s adalah folder tersimpan; isinya bisa diubah, foldernya diatur di tab Lanjutan", rel)
	case holdsSaved(app, rel):
		return invalid("%s berisi folder tersimpan; folder ini tidak bisa dihapus, diganti namanya, atau dipindah", rel)
	}
	return nil
}

// ListFiles lists one folder of the app's workspace.
func (s *Service) ListFiles(ctx context.Context, actor store.User, appID int64, rel string) ([]FileEntry, error) {
	app, owner, err := s.load(ctx, actor, appID)
	if err != nil {
		return nil, err
	}
	if rel, err = cleanRel(rel); err != nil {
		return nil, err
	}
	root, _, name, err := s.workspaceRoot(owner, app, rel)
	if err != nil {
		return nil, fileErr(err)
	}
	defer root.Close()
	fi, err := root.Lstat(name)
	if err != nil {
		return nil, fileErr(err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, errOutside
	}
	if !fi.IsDir() {
		return nil, invalid("%s bukan folder", rel)
	}
	dir, err := root.Open(name)
	if err != nil {
		return nil, fileErr(err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		child := joinRel(rel, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		fe := FileEntry{Name: e.Name(), Type: "file", Size: info.Size(), ModTime: info.ModTime().UnixMilli(), Saved: isSaved(app, child)}
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			fe.Type, fe.Size = "link", 0
		case e.IsDir():
			fe.Type, fe.Size = "dir", 0
			fe.Items = s.countItems(owner, app, child)
		}
		out = append(out, fe)
	}
	slices.SortFunc(out, func(a, b FileEntry) int {
		if (a.Type == "dir") != (b.Type == "dir") {
			if a.Type == "dir" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// countItems counts the entries of one workspace folder; 0 when it cannot be read.
func (s *Service) countItems(owner store.User, app store.App, rel string) int {
	root, _, name, err := s.workspaceRoot(owner, app, rel)
	if err != nil {
		return 0
	}
	defer root.Close()
	d, err := root.Open(name)
	if err != nil {
		return 0
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)
	return len(names)
}

// OpenFile opens one regular file of the app's workspace for download.
func (s *Service) OpenFile(ctx context.Context, actor store.User, appID int64, rel string) (*os.File, fs.FileInfo, error) {
	app, owner, err := s.load(ctx, actor, appID)
	if err != nil {
		return nil, nil, err
	}
	if rel, err = cleanRel(rel); err != nil {
		return nil, nil, err
	}
	if rel == "" {
		return nil, nil, invalid("Pilih file yang akan diunduh")
	}
	return s.openRegular(owner, app, rel)
}

// openRegular opens a regular file of the workspace; a link is refused, not followed.
func (s *Service) openRegular(owner store.User, app store.App, rel string) (*os.File, fs.FileInfo, error) {
	root, _, name, err := s.workspaceRoot(owner, app, rel)
	if err != nil {
		return nil, nil, fileErr(err)
	}
	defer root.Close()
	if fi, err := root.Lstat(name); err != nil {
		return nil, nil, fileErr(err)
	} else if !fi.Mode().IsRegular() {
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil, nil, errOutside
		}
		return nil, nil, invalid("%s bukan file", rel)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, nil, fileErr(err)
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, invalid("%s bukan file", rel)
	}
	return f, fi, nil
}

// DeleteFiles removes files and folders with everything in them. Every path
// is checked first, so one refused path removes nothing. The workspace, the
// saved folders and the folders holding them stay; their contents can go.
// A link is removed itself, never what it points to.
func (s *Service) DeleteFiles(ctx context.Context, actor store.User, appID int64, paths []string) error {
	app, owner, err := s.load(ctx, actor, appID)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return invalid("Pilih file yang akan dihapus")
	}
	list := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := cleanRel(p)
		if err != nil {
			return err
		}
		if err := savedRefusal(app, rel); err != nil {
			return err
		}
		root, _, name, err := s.workspaceRoot(owner, app, rel)
		if err != nil {
			return fileErr(err)
		}
		_, err = root.Lstat(name)
		root.Close()
		if err != nil {
			return fileErr(err)
		}
		if !slices.Contains(list, rel) {
			list = append(list, rel)
		}
	}
	for _, rel := range list {
		root, _, name, err := s.workspaceRoot(owner, app, rel)
		if err != nil {
			return fileErr(err)
		}
		err = root.RemoveAll(name)
		root.Close()
		if err != nil {
			s.remeasure(ctx, owner, app)
			return fileErr(err)
		}
		s.logActivity(ctx, actor, store.ActFileDelete, app.Name, rel)
	}
	s.remeasure(ctx, owner, app)
	return nil
}

// MakeDir creates the folder dir/name, owned by the app's UID.
func (s *Service) MakeDir(ctx context.Context, actor store.User, appID int64, dir, name string) error {
	app, owner, err := s.load(ctx, actor, appID)
	if err != nil {
		return err
	}
	if dir, err = cleanRel(dir); err != nil {
		return err
	}
	if err := validName(name, "folder"); err != nil {
		return err
	}
	rel := joinRel(dir, name)
	root, base, inner, err := s.workspaceRoot(owner, app, rel)
	if err != nil {
		return fileErr(err)
	}
	defer root.Close()
	if inner == "." {
		return invalid("%s sudah ada", name)
	}
	if fi, err := root.Lstat(path.Dir(inner)); err != nil {
		return fileErr(err)
	} else if !fi.IsDir() {
		return errOutside
	}
	if err := root.Mkdir(inner, dirMode); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return invalid("%s sudah ada", name)
		}
		return fileErr(err)
	}
	if err := s.chownIn(root, base, inner, containerUID(owner)); err != nil {
		root.Remove(inner)
		return err
	}
	s.logActivity(ctx, actor, store.ActFileMkdir, app.Name, rel)
	return nil
}

// MoveFile renames or moves from to to inside the workspace, also between the
// data folder and a mounted folder. It never replaces an existing file, never
// moves a saved folder (or one holding it), and never moves through a link.
func (s *Service) MoveFile(ctx context.Context, actor store.User, appID int64, from, to string) error {
	app, owner, err := s.load(ctx, actor, appID)
	if err != nil {
		return err
	}
	if from, err = cleanRel(from); err != nil {
		return err
	}
	if to, err = cleanRel(to); err != nil {
		return err
	}
	if err := savedRefusal(app, from); err != nil {
		return err
	}
	if to == "" || holdsSaved(app, to) {
		return invalid("%s sudah ada", to)
	}
	if err := validName(path.Base(to), "baru"); err != nil {
		return err
	}
	if under(to, from) {
		return invalid("Folder tidak bisa dipindah ke dalam dirinya sendiri")
	}
	src, err := s.openParent(owner, app, from)
	if err != nil {
		return err
	}
	defer src.close()
	if _, err := src.root.Lstat(src.inner); err != nil {
		return fileErr(err)
	}
	dst, err := s.openParent(owner, app, to)
	if err != nil {
		return err
	}
	defer dst.close()
	if err := renameNoReplace(src.dir, path.Base(src.inner), dst.dir, path.Base(dst.inner)); err != nil {
		switch {
		case errors.Is(err, fs.ErrExist):
			return invalid("%s sudah ada", to)
		case errors.Is(err, errCrossDevice):
			return invalid("%s tidak bisa dipindah ke %s: keduanya di disk yang berbeda", from, to)
		}
		return fileErr(err)
	}
	s.logActivity(ctx, actor, store.ActFileRename, app.Name, from+" → "+to)
	s.remeasure(ctx, owner, app)
	return nil
}

// parentDir is the opened folder that holds a workspace path.
type parentDir struct {
	root  *os.Root
	dir   *os.File
	inner string
}

func (p parentDir) close() {
	p.dir.Close()
	p.root.Close()
}

// openParent opens the folder holding rel inside its host folder. The folder
// must be a real folder in the workspace, not a link.
func (s *Service) openParent(owner store.User, app store.App, rel string) (parentDir, error) {
	root, _, inner, err := s.workspaceRoot(owner, app, rel)
	if err != nil {
		return parentDir{}, fileErr(err)
	}
	if inner == "." {
		root.Close()
		return parentDir{}, invalid("%s adalah folder tersimpan", rel)
	}
	parent := path.Dir(inner)
	if fi, err := root.Lstat(parent); err != nil {
		root.Close()
		return parentDir{}, fileErr(err)
	} else if !fi.IsDir() {
		root.Close()
		return parentDir{}, errOutside
	}
	dir, err := root.Open(parent)
	if err != nil {
		root.Close()
		return parentDir{}, fileErr(err)
	}
	return parentDir{root, dir, inner}, nil
}

// SaveFile writes r as dir/name in the app's workspace, owned by the app's
// UID. It may not grow the workspace past the app's disk size; an existing
// file of that name is replaced only after the new one is complete.
func (s *Service) SaveFile(ctx context.Context, actor store.User, appID int64, dir, name string, r io.Reader) error {
	app, owner, err := s.load(ctx, actor, appID)
	if err != nil {
		return err
	}
	if dir, err = cleanRel(dir); err != nil {
		return err
	}
	if err := validName(name, "file"); err != nil {
		return err
	}
	rel := joinRel(dir, name)
	if isSaved(app, rel) {
		return invalid("%s adalah folder tersimpan", rel)
	}
	root, base, inner, err := s.workspaceRoot(owner, app, rel)
	if err != nil {
		return fileErr(err)
	}
	defer root.Close()
	parent := path.Dir(inner)
	if fi, err := root.Lstat(parent); err != nil {
		return fileErr(err)
	} else if !fi.IsDir() {
		return errOutside
	}
	if fi, err := root.Lstat(inner); err == nil && !fi.Mode().IsRegular() {
		return invalid("%s sudah ada dan bukan file", rel)
	}
	limit := int64(-1)
	if app.DiskMB > 0 {
		used, err := measure(ctx, s.workspaceDirs(owner, app)...)
		if err != nil {
			return err
		}
		if limit = int64(app.DiskMB)<<20 - used; limit < 0 {
			limit = 0
		}
	}
	tmp := path.Join(parent, ".runova-upload-"+fmt.Sprint(s.now().UnixNano()))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return fileErr(err)
	}
	src := r
	if limit >= 0 {
		src = io.LimitReader(r, limit+1)
	}
	n, err := io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && limit >= 0 && n > limit {
		err = overQuota("File melebihi sisa disk aplikasi (sisa %d MB dari %d MB)", limit>>20, app.DiskMB)
	}
	if err == nil {
		err = root.Rename(tmp, inner)
	}
	if err == nil {
		if err = s.chownIn(root, base, inner, containerUID(owner)); err != nil {
			root.Remove(inner)
		}
	}
	if err != nil {
		root.Remove(tmp)
		return fileErr(err)
	}
	s.logActivity(ctx, actor, store.ActFileUpload, app.Name, rel)
	s.remeasure(ctx, owner, app)
	return nil
}

// remeasure updates the workspace size after the panel changed files, so the bar is right at once.
func (s *Service) remeasure(ctx context.Context, owner store.User, app store.App) {
	if _, _, err := s.measureApp(ctx, owner, app); err != nil {
		slog.Warn("measure after file change", "app", app.Name, "err", err)
	}
}
