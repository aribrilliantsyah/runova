package artifact

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInspectTarGzMergesEnv(t *testing.T) {
	app, err := os.ReadFile(buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, "", "os.Getenv", "DB_URL", "JWT_SECRET", "PORT"))
	if err != nil {
		t.Fatal(err)
	}
	tgz := buildTarGz(t, reg("app", app), reg(".env.example", []byte("PORT=7000\nDB_URL=postgres://x\n")))
	work := t.TempDir()
	rep, kept, err := Inspect(tgz, "release.tar.gz", work, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Kind != KindStatic || rep.Port != 7000 || rep.PortSource != SourceEnvExample || !rep.OK() {
		t.Errorf("report = %+v", rep)
	}
	byName := map[string]EnvVar{}
	for _, v := range rep.Env {
		byName[v.Name] = v
	}
	if got := envNames(rep.Env); !slices.Equal(got, []string{"DB_URL", "JWT_SECRET", "PORT"}) {
		t.Fatalf("env = %v", got)
	}
	if v := byName["DB_URL"]; v.Source != SourceEnvExample || v.Default != "postgres://x" {
		t.Errorf("DB_URL = %+v", v)
	}
	if v := byName["JWT_SECRET"]; v.Source != SourceScan || !v.Secret {
		t.Errorf("JWT_SECRET = %+v", v)
	}
	if kept != filepath.Join(work, "app") {
		t.Errorf("kept = %q", kept)
	}
	sum := sha256.Sum256(app)
	if rep.FileName != "app" || rep.Size != int64(len(app)) || rep.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("identity = %q %d %s", rep.FileName, rep.Size, rep.SHA256)
	}
}

func TestInspectSecretIsOredAcrossSources(t *testing.T) {
	jar := appJar(t, 61, map[string][]byte{"application.properties": []byte("x=${DB_CRED}\ny=${API_TOKEN:}")})
	data, err := os.ReadFile(jar)
	if err != nil {
		t.Fatal(err)
	}
	tgz := buildTarGz(t, reg("app.jar", data), reg(".env.example", []byte("# secret\nDB_CRED=abc\nAPI_TOKEN=\n")))
	rep, _, err := Inspect(tgz, "r.tar.gz", t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Kind != KindJar || rep.Port != 8080 {
		t.Fatalf("report = %+v", rep)
	}
	for _, v := range rep.Env {
		if v.Source != SourceEnvExample || !v.Secret {
			t.Errorf("%+v", v)
		}
	}
	if len(rep.Env) != 2 || rep.Env[1].Name != "DB_CRED" || rep.Env[1].Default != "abc" {
		t.Errorf("env = %+v", rep.Env)
	}
}

func TestInspectJarKeepsPathAndDescribes(t *testing.T) {
	jar := appJar(t, 61, nil)
	rep, kept, err := Inspect(jar, "my app.jar", t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(jar)
	if kept != jar || rep.FileName != "my app.jar" || rep.Size != st.Size() || len(rep.SHA256) != 64 {
		t.Errorf("kept=%q report=%+v", kept, rep)
	}
}

func TestInspectPortOnlyFromEnvExampleForBinaries(t *testing.T) {
	jar := appJar(t, 61, nil)
	data, _ := os.ReadFile(jar)
	tgz := buildTarGz(t, reg("app.jar", data), reg(".env.example", []byte("PORT=7000\n")))
	rep, _, err := Inspect(tgz, "r.tar.gz", t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Port != 8080 || rep.PortSource != "bawaan" {
		t.Errorf("jar port = %d (%s)", rep.Port, rep.PortSource)
	}

	bin, _ := os.ReadFile(buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, ""))
	for _, port := range []string{"abc", "0", "70000", ""} {
		tgz := buildTarGz(t, reg("app", bin), reg(".env.example", []byte("PORT="+port+"\n")))
		rep, _, err := Inspect(tgz, "r.tar.gz", t.TempDir(), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Port != 8080 || rep.PortSource != "bawaan" {
			t.Errorf("PORT=%q: port = %d (%s)", port, rep.Port, rep.PortSource)
		}
	}
}

func TestInspectUnknown(t *testing.T) {
	rep, _, err := Inspect(writeFile(t, "notes.txt", []byte("just some text")), "notes.txt", t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(rep, "Jenis file tidak dikenali. Unggah .jar, binary Linux, atau .tar.gz") || rep.OK() {
		t.Errorf("problems = %v", rep.Problems)
	}
}

func TestInspectBadArchiveReportsProblem(t *testing.T) {
	work := t.TempDir()
	tgz := buildTarGz(t, reg("../evil", []byte("x")))
	rep, kept, err := Inspect(tgz, "a.tar.gz", work, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(rep, "Arsip berisi path tidak aman") || kept != "" {
		t.Errorf("problems=%v kept=%q", rep.Problems, kept)
	}
	if got := dirEntries(t, work); len(got) != 0 {
		t.Errorf("work dir: %v", got)
	}
}

func TestInspectWindowsAndMachoNamed(t *testing.T) {
	rep, _, err := Inspect(writeFile(t, "a.exe", []byte("MZ\x90\x00")), "a.exe", t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(rep, "Windows") {
		t.Errorf("problems = %v", rep.Problems)
	}
}

func TestReportJSONShape(t *testing.T) {
	rep, _, err := Inspect(appJar(t, 61, nil), "x.jar", t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(rep)
	for _, key := range []string{`"kind":"jar"`, `"file_name":"x.jar"`, `"min_java":17`, `"main_class":"app.Main"`, `"port":8080`, `"port_source":"bawaan"`, `"env":[]`, `"problems":[]`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("json missing %s: %s", key, b)
		}
	}
}
