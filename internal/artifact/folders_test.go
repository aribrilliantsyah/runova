package artifact

import (
	"debug/elf"
	"os"
	"slices"
	"testing"

	"runova/internal/folders"
)

func TestDetectFolderPositive(t *testing.T) {
	for _, c := range []struct{ key, value, want string }{
		{"STORAGE_PATH", "./storage", "storage"},
		{"UPLOAD_DIR", "uploads", "uploads"},
		{"UPLOAD_DIR", "/app/storage", "/app/storage"},
		{"FILES_ROOT", "/var/www/uploads", "/var/www/uploads"},
		{"MEDIA_ROOT", "./media/", "media"},
		{"ATTACHMENTS", "./attachments", "attachments"},
		{"IMAGE_DIR", "public/images", "public/images"},
		{"DATA_DIR", "./data", "data"},
		{"APP_HOME", "/srv/app/public/uploads", "/srv/app/public/uploads"},
		{"spring.servlet.multipart.location", "/opt/app/tmpfiles", "/opt/app/tmpfiles"},
		{"server.tomcat.basedir", "./tomcat", "tomcat"},
		{"app.upload-dir", "${UPLOAD_DIR:./uploads}", "uploads"},
		{"app.uploadDir", "files", "files"},
		{"app.storage.base-path", "/srv/blobs", "/srv/blobs"},
		{"app.file.target-dir", "./out", "out"},
		{"starboom.storage.path", "${user.dir}/storage", "storage"},
		{"app.upload.location", "file:./uploads", "uploads"},
		{"BACKUP", "\"./storage/backup\"", "storage/backup"},
		{"X", "/data/storage", "storage"},
	} {
		got, ok := detectFolder(c.key, c.value)
		if !ok || got.Path != c.want {
			t.Errorf("detectFolder(%q, %q) = %+v, %v; want %q", c.key, c.value, got, ok, c.want)
		}
	}
}

func TestDetectFolderNegative(t *testing.T) {
	for _, c := range []struct{ key, value string }{
		{"STORAGE_PATH", ""},
		{"UPLOAD_DIR", "/tmp/uploads"},                // scratch tmpfs, not saved
		{"UPLOAD_DIR", "/etc/uploads"},                // system folder
		{"UPLOAD_DIR", "../uploads"},                  // leaves the workspace
		{"MEDIA_URL", "/media/"},                      // a URL path, not a folder
		{"UPLOAD_ENDPOINT", "/api/uploads"},           // a route
		{"STATIC_PREFIX", "/files"},                   // a route
		{"S3_BUCKET", "my-uploads"},                   // not a path, no folder key
		{"STORAGE_DRIVER", "local"},                   // a setting, not a path
		{"DATABASE_URL", "postgres://x/data"},         // URL
		{"PGDATA", "/var/lib/postgresql/data"},        // the database's own folder
		{"DB_PATH", "./data.db"},                      // a file under the database key
		{"LOG_LEVEL", "debug"},                        // no path
		{"APP_NAME", "files"},                         // bare word, key is not a folder key
		{"HOME", "/home/app"},                         // no folder word
		{"spring.datasource.url", "jdbc:h2:./data/x"}, // a JDBC URL
		{"app.storage.path", "${STORAGE_PATH}"},       // no default: unknown
		{"app.storage.path", "${HOME}/storage"},       // unresolved variable
		{"server.port", "8080"},
		{"UPLOAD_DIR", "s3://bucket/uploads"},
		{"FILES", "C:\\uploads"},
		{"STORAGE_PATH", "."},
		{"STORAGE_PATH", "/data"},
	} {
		if got, ok := detectFolder(c.key, c.value); ok {
			t.Errorf("detectFolder(%q, %q) = %+v, want no folder", c.key, c.value, got)
		}
	}
}

func TestJarDetectsFoldersFromPropertiesAndYaml(t *testing.T) {
	props := "spring.servlet.multipart.location=/app/storage\napp.upload-dir=${UPLOAD_DIR:./uploads}\n# app.media=./media\nserver.port=8080\n"
	yml := "app:\n  storage:\n    base-path: ./storage\n  name: x\nspring:\n  datasource:\n    url: jdbc:h2:./data/x\n"
	rep, err := InspectJar(appJar(t, 61, map[string][]byte{
		"BOOT-INF/classes/application.properties": []byte(props),
		"BOOT-INF/classes/application.yml":        []byte(yml),
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := []DetectedFolder{
		{Folder: folders.Folder{Path: "/app/storage", Dir: "storage-2"}, Source: "application.properties", Key: "spring.servlet.multipart.location"},
		{Folder: folders.Folder{Path: "uploads", Dir: "uploads"}, Source: "application.properties", Key: "app.upload-dir"},
		{Folder: folders.Folder{Path: "storage", Dir: "storage"}, Source: "application.yml", Key: "app.storage.base-path"},
	}
	if !slices.Equal(rep.Folders, want) {
		t.Fatalf("folders = %+v\nwant      %+v", rep.Folders, want)
	}
}

func TestInspectTarGzFoldersFromEnvExampleFirst(t *testing.T) {
	app, err := os.ReadFile(buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, "", "os.Getenv", "STORAGE_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	env := "PORT=7000\nSTORAGE_PATH=./storage\nUPLOAD_DIR=/var/www/uploads\nMEDIA_URL=/media/\nZZ_STORAGE=storage/\n"
	tgz := buildTarGz(t, reg("app", app), reg(".env.example", []byte(env)))
	rep, _, err := Inspect(tgz, "r.tar.gz", t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	want := []DetectedFolder{
		{Folder: folders.Folder{Path: "storage", Dir: "storage"}, Source: SourceEnvExample, Key: "STORAGE_PATH"},
		{Folder: folders.Folder{Path: "/var/www/uploads", Dir: "uploads"}, Source: SourceEnvExample, Key: "UPLOAD_DIR"},
	}
	if !slices.Equal(rep.Folders, want) {
		t.Fatalf("folders = %+v\nwant      %+v", rep.Folders, want)
	}
}

func TestReportFoldersMarshalAsArray(t *testing.T) {
	rep, _, err := Inspect(appJar(t, 61, nil), "x.jar", t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Folders == nil {
		t.Fatal("folders is nil; the panel expects []")
	}
}

func TestYamlPairs(t *testing.T) {
	yml := "app:\n  storage:\n    path: ./a\n  list:\n    - x\n  name: \"y\"\nother: z\n"
	got := yamlPairs(configLines(yml, true))
	want := [][2]string{{"app.storage.path", "./a"}, {"app.name", "y"}, {"other", "z"}}
	if !slices.Equal(got, want) {
		t.Fatalf("pairs = %v", got)
	}
}
