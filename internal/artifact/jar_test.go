package artifact

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func appJar(t *testing.T, major uint16, extra map[string][]byte) string {
	t.Helper()
	files := map[string][]byte{
		"META-INF/MANIFEST.MF": manifest("Main-Class", "app.Main"),
		"app/Main.class":       classBytes(major),
	}
	for k, v := range extra {
		files[k] = v
	}
	return buildJar(t, files)
}

func TestJarMinJavaMapping(t *testing.T) {
	cases := []struct {
		major uint16
		want  int
	}{
		{45, 8}, {52, 8}, {53, 11}, {55, 11}, {56, 17}, {57, 17}, {61, 17},
		{62, 21}, {65, 21}, {66, 25}, {69, 25},
	}
	for _, c := range cases {
		rep, err := InspectJar(appJar(t, c.major, nil))
		if err != nil {
			t.Fatal(err)
		}
		if rep.MinJava != c.want || !rep.OK() || rep.Kind != KindJar {
			t.Errorf("major %d: kind=%q min_java=%d problems=%v, want %d", c.major, rep.Kind, rep.MinJava, rep.Problems, c.want)
		}
	}
}

func TestJarTooNew(t *testing.T) {
	rep, err := InspectJar(appJar(t, 70, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(rep, "Butuh Java lebih baru dari 25") || rep.OK() {
		t.Errorf("problems = %v", rep.Problems)
	}
	if rep.MinJava != 26 {
		t.Errorf("MinJava = %d, want 26", rep.MinJava)
	}
}

func TestJarIgnoresMultiRelease(t *testing.T) {
	rep, err := InspectJar(appJar(t, 52, map[string][]byte{"META-INF/versions/21/app/X.class": classBytes(65)}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.MinJava != 8 {
		t.Errorf("MinJava = %d, want 8", rep.MinJava)
	}
}

func TestJarNestedLibRaisesMinimum(t *testing.T) {
	lib := zipBytes(t, map[string][]byte{
		"META-INF/versions/25/lib/Z.class": classBytes(69),
		"lib/A.class":                      classBytes(61),
	})
	path := buildJar(t, map[string][]byte{
		"META-INF/MANIFEST.MF":            manifest("Main-Class", "org.springframework.boot.loader.launch.JarLauncher", "Start-Class", "app.Main"),
		"BOOT-INF/classes/app/Main.class": classBytes(52),
		"BOOT-INF/lib/a.jar":              lib,
	})
	rep, err := InspectJar(path)
	if err != nil {
		t.Fatal(err)
	}
	if rep.MinJava != 17 {
		t.Errorf("MinJava = %d, want 17", rep.MinJava)
	}
	if rep.MainClass != "app.Main" {
		t.Errorf("MainClass = %q", rep.MainClass)
	}
	if !rep.OK() {
		t.Errorf("problems = %v", rep.Problems)
	}
}

func TestJarMissingMainClass(t *testing.T) {
	path := buildJar(t, map[string][]byte{
		"META-INF/MANIFEST.MF": manifest(),
		"app/Main.class":       classBytes(52),
	})
	rep, err := InspectJar(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(rep, "Manifest tidak punya Main-Class") {
		t.Errorf("problems = %v", rep.Problems)
	}
}

func TestJarNoManifest(t *testing.T) {
	rep, err := InspectJar(buildJar(t, map[string][]byte{"app/Main.class": classBytes(52)}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(rep, "Manifest tidak punya Main-Class") {
		t.Errorf("problems = %v", rep.Problems)
	}
}

func TestJarNotZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.jar")
	if err := os.WriteFile(path, []byte("definitely not a zip archive, just text"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := InspectJar(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(rep, "Bukan file JAR yang valid") {
		t.Errorf("problems = %v", rep.Problems)
	}
}

func TestJarMissingFileIsIOError(t *testing.T) {
	if _, err := InspectJar(filepath.Join(t.TempDir(), "absent.jar")); err == nil {
		t.Error("want error for missing file")
	}
}

func TestJarNoClasses(t *testing.T) {
	rep, err := InspectJar(buildJar(t, map[string][]byte{"META-INF/MANIFEST.MF": manifest("Main-Class", "app.Main")}))
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(rep, "JAR tidak berisi kelas Java") {
		t.Errorf("problems = %v", rep.Problems)
	}
}

func TestJarPortAndEnvFromProperties(t *testing.T) {
	props := "server.port=${PORT:9000}\nspring.datasource.url=${DB_URL}\napp.jwt=${JWT_SECRET:}\n# ${COMMENTED_OUT}\napp.name=${spring.application.name}\n"
	rep, err := InspectJar(appJar(t, 61, map[string][]byte{"application.properties": []byte(props)}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Port != 9000 || rep.PortSource != "application.properties" {
		t.Errorf("port = %d (%s)", rep.Port, rep.PortSource)
	}
	if got := envNames(rep.Env); !slices.Equal(got, []string{"DB_URL", "JWT_SECRET", "PORT"}) {
		t.Fatalf("env = %v", got)
	}
	for _, e := range rep.Env {
		if e.Source != SourceProperties || e.Default != "" {
			t.Errorf("%s: source=%q default=%q", e.Name, e.Source, e.Default)
		}
		if e.Secret != (e.Name == "JWT_SECRET") {
			t.Errorf("%s: secret=%v", e.Name, e.Secret)
		}
	}
}

func TestJarPropertiesInBootInfClasses(t *testing.T) {
	rep, err := InspectJar(appJar(t, 61, map[string][]byte{
		"BOOT-INF/classes/application.properties": []byte("server.port = 7777\n"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Port != 7777 {
		t.Errorf("port = %d", rep.Port)
	}
}

func TestJarPortFromYaml(t *testing.T) {
	yml := "spring:\n  application:\n    name: x\nserver:\n  ssl:\n    port: 1\n  port: 8181\nother:\n  port: 5\n"
	rep, err := InspectJar(appJar(t, 61, map[string][]byte{"application.yml": []byte(yml)}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Port != 8181 || rep.PortSource != "application.yml" {
		t.Errorf("port = %d (%s)", rep.Port, rep.PortSource)
	}
}

func TestJarYamlPlaceholders(t *testing.T) {
	yml := "server:\n  port: ${PORT:9100} # comment ${IGNORED}\nspring:\n  datasource:\n    url: ${DB_URL}\n"
	rep, err := InspectJar(appJar(t, 61, map[string][]byte{"application.yml": []byte(yml)}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Port != 9100 {
		t.Errorf("port = %d", rep.Port)
	}
	if got := envNames(rep.Env); !slices.Equal(got, []string{"DB_URL", "PORT"}) {
		t.Errorf("env = %v", got)
	}
}

func TestJarDefaultPort(t *testing.T) {
	rep, err := InspectJar(appJar(t, 61, nil))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Port != 8080 || rep.PortSource != "bawaan" {
		t.Errorf("port = %d (%s)", rep.Port, rep.PortSource)
	}
	if rep.Env == nil || rep.Problems == nil {
		t.Error("Env and Problems must be non-nil so JSON renders arrays")
	}
}

func TestJarPortPlaceholderWithoutDefault(t *testing.T) {
	rep, err := InspectJar(appJar(t, 61, map[string][]byte{"application.properties": []byte("server.port=${PORT}\n")}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Port != 8080 || rep.PortSource != "bawaan" {
		t.Errorf("port = %d (%s)", rep.Port, rep.PortSource)
	}
}
