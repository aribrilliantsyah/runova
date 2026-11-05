// Package folders holds the rules for an app's saved folders: paths inside
// the container that keep their files across deploys because they live in the
// app's workspace (its data folder on the host). A relative path is already
// inside the workspace (the container's working directory is /data); an
// absolute path elsewhere is bind-mounted from a workspace subfolder.
package folders

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	// DataMount is where the workspace appears in the container (and its working directory).
	DataMount = "/data"
	// AppMount holds the artifact, read-only.
	AppMount = "/app"
	// MaxFolders caps the saved folders of one app.
	MaxFolders = 20
	maxPathLen = 200
)

// Folder is one saved folder. Path is where the app writes: relative to the
// workspace ("storage") or absolute outside it ("/app/storage"). Dir is the
// workspace subfolder that holds the files; for a relative Path it is Path.
type Folder struct {
	Path string `json:"path"`
	Dir  string `json:"dir"`
}

// Mount reports whether the folder needs a bind mount (its path is outside the workspace).
func (f Folder) Mount() bool { return strings.HasPrefix(f.Path, "/") }

// ContainerPath is the absolute path of the folder inside the container.
func (f Folder) ContainerPath() string {
	if f.Mount() {
		return f.Path
	}
	return DataMount + "/" + f.Path
}

var (
	pathRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	dirRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

	// blockedExact are directories that may not be replaced as a whole.
	blockedExact = []string{"/", "/var", "/opt", "/srv", "/home", "/mnt", "/media", AppMount, DataMount, "/tmp"}
	// blockedTree are directories nothing may be mounted in or under.
	blockedTree = []string{"/proc", "/sys", "/dev", "/etc", "/usr", "/bin", "/sbin", "/boot", "/run", "/root", "/var/run", "/tmp", "/opt/java"}
)

// under reports whether p is dir or inside it.
func under(p, dir string) bool {
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}

// Parse normalizes a path the app writes to. Relative paths and paths under
// /data become workspace-relative ("storage"); other absolute paths stay
// absolute. System directories, /tmp (already a scratch tmpfs), the workspace
// root itself and any path with "..", spaces or shell syntax are refused.
func Parse(raw string) (Folder, error) {
	p := strings.TrimSpace(raw)
	switch {
	case p == "":
		return Folder{}, errors.New("Path folder kosong")
	case len(p) > maxPathLen:
		return Folder{}, fmt.Errorf("Path folder maksimal %d karakter", maxPathLen)
	case !pathRE.MatchString(p):
		return Folder{}, fmt.Errorf("Path %q hanya boleh huruf, angka, titik, garis bawah, tanda hubung, dan /", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return Folder{}, fmt.Errorf("Path %q tidak boleh memakai ..", p)
		}
	}
	if !strings.HasPrefix(p, "/") {
		rel := path.Clean(p)
		if rel == "." {
			return Folder{}, errors.New("Folder kerja aplikasi sudah tersimpan seluruhnya; pilih subfolder, misalnya storage")
		}
		return Folder{Path: rel, Dir: rel}, nil
	}
	abs := path.Clean(p)
	if under(abs, DataMount) {
		rel := strings.TrimPrefix(strings.TrimPrefix(abs, DataMount), "/")
		if rel == "" {
			return Folder{}, errors.New("/data sudah tersimpan seluruhnya; pilih subfolder, misalnya /data/storage")
		}
		return Folder{Path: rel, Dir: rel}, nil
	}
	if slices.Contains(blockedExact, abs) {
		return Folder{}, fmt.Errorf("%s tidak bisa dijadikan folder tersimpan; pilih subfolder yang dipakai aplikasi", abs)
	}
	if under(abs, "/tmp") {
		return Folder{}, fmt.Errorf("%s ada di /tmp, yang sengaja dikosongkan tiap restart", abs)
	}
	for _, b := range blockedTree {
		if under(abs, b) {
			return Folder{}, fmt.Errorf("%s adalah folder sistem dan tidak bisa di-mount", abs)
		}
	}
	if strings.HasPrefix(abs, "/lib") {
		return Folder{}, fmt.Errorf("%s adalah folder sistem dan tidak bisa di-mount", abs)
	}
	return Folder{Path: abs, Dir: path.Base(abs)}, nil
}

// Check validates and normalizes an app's folder list against the artifact
// file name (mounted at /app/<file>). A missing Dir of an absolute path
// defaults to its last element. Paths and workspace folders must not overlap.
func Check(list []Folder, artifactFile string) ([]Folder, error) {
	if len(list) > MaxFolders {
		return nil, fmt.Errorf("Folder tersimpan maksimal %d", MaxFolders)
	}
	out := make([]Folder, 0, len(list))
	artifact := AppMount + "/" + artifactFile
	for _, in := range list {
		f, err := Parse(in.Path)
		if err != nil {
			return nil, err
		}
		if dir := strings.TrimSpace(in.Dir); dir != "" {
			if !f.Mount() {
				if dir != f.Dir {
					return nil, fmt.Errorf("%s sudah ada di folder kerja; folder workspace-nya harus %s", f.Path, f.Dir)
				}
			} else if !dirRE.MatchString(dir) {
				return nil, fmt.Errorf("Folder workspace %q harus satu nama folder: huruf, angka, titik, garis bawah, tanda hubung", dir)
			}
			f.Dir = dir
		}
		if f.Mount() && artifactFile != "" && (under(f.Path, artifact) || under(artifact, f.Path)) {
			return nil, fmt.Errorf("%s menimpa file aplikasi %s", f.Path, artifact)
		}
		for _, o := range out {
			switch {
			case f.ContainerPath() == o.ContainerPath():
				return nil, fmt.Errorf("Path %s muncul lebih dari sekali", f.ContainerPath())
			case under(f.ContainerPath(), o.ContainerPath()), under(o.ContainerPath(), f.ContainerPath()):
				return nil, fmt.Errorf("%s dan %s saling bertumpuk; pilih salah satu", o.ContainerPath(), f.ContainerPath())
			case under(f.Dir, o.Dir), under(o.Dir, f.Dir):
				return nil, fmt.Errorf("%s dan %s memakai folder workspace yang sama (%s)", o.ContainerPath(), f.ContainerPath(), o.Dir)
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// UniqueDir returns base, or base-2, base-3… when taken already has it.
func UniqueDir(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		if c := base + "-" + strconv.Itoa(i); !taken[c] {
			return c
		}
	}
}
