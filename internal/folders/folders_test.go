package folders

import (
	"strings"
	"testing"
)

func TestParseAccepts(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Folder
	}{
		{"./storage", Folder{Path: "storage", Dir: "storage"}},
		{"storage/", Folder{Path: "storage", Dir: "storage"}},
		{"uploads", Folder{Path: "uploads", Dir: "uploads"}},
		{"./public/uploads/", Folder{Path: "public/uploads", Dir: "public/uploads"}},
		{" storage ", Folder{Path: "storage", Dir: "storage"}},
		{"/data/storage", Folder{Path: "storage", Dir: "storage"}},
		{"/data/a/b", Folder{Path: "a/b", Dir: "a/b"}},
		{"/app/storage", Folder{Path: "/app/storage", Dir: "storage"}},
		{"/var/www/uploads/", Folder{Path: "/var/www/uploads", Dir: "uploads"}},
		{"/srv/app//files", Folder{Path: "/srv/app/files", Dir: "files"}},
		{"/opt/starboom/media", Folder{Path: "/opt/starboom/media", Dir: "media"}},
		{"/home/app/Uploads", Folder{Path: "/home/app/Uploads", Dir: "Uploads"}},
	} {
		got, err := Parse(c.in)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"", ".", "./", "..", "../x", "a/../../x", "a/../b", "/app/../etc",
		"/", "/proc", "/proc/self", "/sys/fs", "/dev", "/dev/shm", "/etc", "/etc/app",
		"/boot", "/run/x", "/root", "/var", "/opt", "/opt/java/openjdk", "/app", "/srv",
		"/data", "/data/", "/tmp", "/tmp/uploads", "/var/run/x", "/home", "/mnt", "/media",
		`C:\uploads`, "a\x00b", "with space", "a:b", "$HOME/x", "~/x",
		strings.Repeat("a", 256),
	} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %+v, want an error", in, got)
		}
	}
}

func TestMount(t *testing.T) {
	if !(Folder{Path: "/app/storage", Dir: "storage"}).Mount() {
		t.Error("an absolute path needs a mount")
	}
	if (Folder{Path: "storage", Dir: "storage"}).Mount() {
		t.Error("a path in the workspace needs no mount")
	}
}

func TestCheckNormalizesAndKeepsOrder(t *testing.T) {
	got, err := Check([]Folder{{Path: "./storage"}, {Path: "/app/media", Dir: "pics"}, {Path: "/var/www/uploads"}}, "app.jar")
	if err != nil {
		t.Fatal(err)
	}
	want := []Folder{{Path: "storage", Dir: "storage"}, {Path: "/app/media", Dir: "pics"}, {Path: "/var/www/uploads", Dir: "uploads"}}
	if len(got) != len(want) {
		t.Fatalf("Check = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Check[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if empty, err := Check(nil, "x"); err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("Check(nil) = %#v, %v; want an empty list", empty, err)
	}
}

func TestCheckRejects(t *testing.T) {
	for name, c := range map[string][]Folder{
		"the artifact itself":     {{Path: "/app/app.jar"}},
		"under the artifact":      {{Path: "/app/app.jar/x"}},
		"duplicate path":          {{Path: "storage"}, {Path: "./storage/"}},
		"nested mounts":           {{Path: "/srv/app"}, {Path: "/srv/app/files"}},
		"same workspace folder":   {{Path: "/app/storage"}, {Path: "/srv/storage"}},
		"mount over relative":     {{Path: "storage/x"}, {Path: "/app/storage"}},
		"relative inside another": {{Path: "storage"}, {Path: "storage/cache"}},
		"dir with slash":          {{Path: "/app/storage", Dir: "a/b"}},
		"dir dot dot":             {{Path: "/app/storage", Dir: ".."}},
		"dir hidden runova":       {{Path: "/app/storage", Dir: ".x"}},
		"relative with other dir": {{Path: "storage", Dir: "other"}},
		"bad path":                {{Path: "/etc"}},
	} {
		if got, err := Check(c, "app.jar"); err == nil {
			t.Errorf("%s: Check = %+v, want an error", name, got)
		}
	}
	many := make([]Folder, MaxFolders+1)
	for i := range many {
		many[i] = Folder{Path: "f" + strings.Repeat("x", i+1)}
	}
	if _, err := Check(many, "a"); err == nil {
		t.Errorf("Check accepted %d folders", len(many))
	}
}

func TestUniqueDir(t *testing.T) {
	taken := map[string]bool{"storage": true, "storage-2": true}
	if got := UniqueDir("storage", taken); got != "storage-3" {
		t.Errorf("UniqueDir = %q", got)
	}
	if got := UniqueDir("media", taken); got != "media" {
		t.Errorf("UniqueDir = %q", got)
	}
}
