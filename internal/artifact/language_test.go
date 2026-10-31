package artifact

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBinaryLanguageMarkers(t *testing.T) {
	cases := []struct {
		name   string
		interp string
		extra  []string
		want   string
	}{
		{"go buildinfo string", "", []string{"\xff Go buildinf:", "libc.so.6"}, LangGo},
		{"rust version", "", []string{"rustc version 1.90.0 (1159e78c4 2025-09-14)"}, LangRust},
		{"rust source path", glibcInterp, []string{"/rustc/1159e78c4/library/std/src/rt.rs", "GCC: (GNU) 16.2.1"}, LangRust},
		{"zig debug build", "", []string{"/usr/lib/zig/std/start.zig"}, LangZig},
		{"zig release build", "", []string{"Linker: LLD 21.1.8"}, LangZig},
		{"cpp libstdc++", glibcInterp, []string{"GLIBCXX_3.4.32", "GCC: (GNU) 16.2.1"}, LangCpp},
		{"c gcc", "", []string{"GCC: (GNU) 16.2.1 20260810"}, LangC},
		{"c clang", glibcInterp, []string{"clang version 20.1.8"}, LangC},
		{"c with lld linker", glibcInterp, []string{"GCC: (GNU) 16.2.1", "Linker: LLD 20.1.8"}, LangC},
		{"dotnet nativeaot", "", []string{"System.Private.CoreLib"}, LangDotnet},
		{"unknown", "", []string{"just some text"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep, err := InspectBinary(buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, c.interp, c.extra...))
			if err != nil {
				t.Fatal(err)
			}
			if rep.Language != c.want {
				t.Errorf("Language = %q, want %q", rep.Language, c.want)
			}
		})
	}
}

func TestBinaryLanguageMarkerAcrossChunkBoundary(t *testing.T) {
	marker := "rustc version 1.90.0"
	pad := make([]byte, scanChunk-len("rustc ver")-200)
	for i := range pad {
		pad[i] = 'a'
	}
	// The ELF image before the padding is 200-odd bytes, so the marker straddles the 1 MiB chunk edge.
	rep, err := InspectBinary(buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, "", string(pad), marker))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Language != LangRust {
		t.Errorf("Language = %q, want rust", rep.Language)
	}
}

// goBinary builds a real Go program; the sections and the buildinfo string
// come from the toolchain, not from the test.
func goBinary(t *testing.T, env []string, flags ...string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "app")
	args := append([]string{"build", "-o", out}, flags...)
	cmd := exec.Command("go", append(args, src)...)
	cmd.Env = append(os.Environ(), append([]string{"GOOS=linux", "GOARCH=amd64"}, env...)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

func TestBinaryLanguageGo(t *testing.T) {
	for name, flags := range map[string][]string{
		"default":  nil,
		"stripped": {"-ldflags=-s -w"},
	} {
		t.Run(name, func(t *testing.T) {
			rep, err := InspectBinary(goBinary(t, []string{"CGO_ENABLED=0"}, flags...))
			if err != nil {
				t.Fatal(err)
			}
			if rep.Kind != KindStatic || rep.Language != LangGo {
				t.Errorf("Kind = %q, Language = %q, want static go", rep.Kind, rep.Language)
			}
		})
	}
}

func TestJarLanguage(t *testing.T) {
	nested := zipBytes(t, map[string][]byte{"lib/A.class": classBytes(52)})
	cases := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"plain java", map[string][]byte{"app/Main.class": classBytes(61)}, LangJava},
		{"kotlin stdlib classes", map[string][]byte{"app/Main.class": classBytes(61), "kotlin/Unit.class": classBytes(61)}, LangKotlin},
		{"kotlin module file", map[string][]byte{"app/Main.class": classBytes(61), "META-INF/app.kotlin_module": nil}, LangKotlin},
		{"kotlin in spring boot", map[string][]byte{"BOOT-INF/classes/kotlin/Unit.class": classBytes(61)}, LangKotlin},
		{"scala", map[string][]byte{"app/Main.class": classBytes(61), "scala/Option.class": classBytes(61)}, LangScala},
		{"scala runtime jar nested", map[string][]byte{"app/Main.class": classBytes(61), "BOOT-INF/lib/scala-library-2.13.12.jar": nested}, LangScala},
		{"groovy", map[string][]byte{"app/Main.class": classBytes(61), "groovy/lang/Closure.class": classBytes(61)}, LangGroovy},
		{"clojure", map[string][]byte{"clojure/core__init.class": classBytes(61)}, LangClojure},
		{"java library bundling kotlin stdlib", map[string][]byte{"BOOT-INF/classes/app/Main.class": classBytes(61), "BOOT-INF/lib/kotlin-stdlib-1.9.0.jar": nested, "BOOT-INF/lib/okhttp-4.12.jar": nested}, LangJava},
		{"kotlin_module deeper than META-INF is not the app's", map[string][]byte{"app/Main.class": classBytes(61), "META-INF/versions/9/x.kotlin_module": nil}, LangJava},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string][]byte{"META-INF/MANIFEST.MF": manifest("Main-Class", "app.Main")}
			for k, v := range c.files {
				files[k] = v
			}
			rep, err := InspectJar(buildJar(t, files))
			if err != nil {
				t.Fatal(err)
			}
			if rep.Language != c.want {
				t.Errorf("Language = %q, want %q", rep.Language, c.want)
			}
		})
	}
}
