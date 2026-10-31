package artifact

import (
	"bytes"
	"debug/elf"
	"io"
	"slices"
	"strings"
)

// Languages an artifact can be written in. The empty string means unknown.
const (
	LangJava    = "java"
	LangKotlin  = "kotlin"
	LangScala   = "scala"
	LangGroovy  = "groovy"
	LangClojure = "clojure"
	LangGo      = "go"
	LangRust    = "rust"
	LangZig     = "zig"
	LangCpp     = "cpp"
	LangC       = "c"
	LangDotnet  = "dotnet"
)

// languageMarkers maps strings found in compiled binaries to the language
// whose toolchain embeds them, in priority order: the first language with a
// marker present wins, because a binary often carries the strings of the
// toolchains it links (a Go program with cgo has GCC's .comment, a Rust one
// has libgcc's, C++ has GCC's too). Zig comes twice: its debug builds name
// the standard library, while release builds keep only the LLD .comment
// entry, which C toolchains also write but beside their own compiler's.
var languageMarkers = []struct {
	lang    string
	markers []string
}{
	{LangGo, []string{"\xff Go buildinf:"}},
	{LangRust, []string{"rustc version", "/rustc/"}},
	{LangZig, []string{"zig/std/", "ZIG_PROGRESS"}},
	{LangDotnet, []string{"System.Private.CoreLib", "DOTNET_gcServer"}},
	{LangCpp, []string{"GLIBCXX", "libstdc++"}},
	{LangC, []string{"GCC: (", "clang version"}},
	{LangZig, []string{"Linker: LLD"}},
}

// binaryLanguage names the language an ELF file was written in, judged from
// its sections, dynamic dependencies and embedded toolchain strings; "" when
// nothing identifies it. The file is only read, never run. r reads the same
// file ef was parsed from.
func binaryLanguage(ef *elf.File, r io.Reader) (string, error) {
	switch {
	case ef.Section(".go.buildinfo") != nil || ef.Section(".note.go.buildid") != nil:
		return LangGo, nil
	case ef.Section(".rustc") != nil:
		return LangRust, nil
	}
	found, err := scanMarkersIn(r)
	if err != nil {
		return "", err
	}
	for i, entry := range languageMarkers {
		if found[i] {
			return entry.lang, nil
		}
	}
	if libs, _ := ef.ImportedLibraries(); slices.ContainsFunc(libs, func(l string) bool { return strings.HasPrefix(l, "libstdc++") }) {
		return LangCpp, nil
	}
	return "", nil
}

// scanMarkersIn streams r and reports, per languageMarkers entry, whether any
// of its markers occurs.
func scanMarkersIn(r io.Reader) ([]bool, error) {
	found := make([]bool, len(languageMarkers))
	maxLen := 0
	for _, e := range languageMarkers {
		for _, m := range e.markers {
			maxLen = max(maxLen, len(m))
		}
	}
	buf := make([]byte, scanChunk)
	var carry []byte
	for {
		n, err := io.ReadFull(r, buf)
		window := append(carry, buf[:n]...)
		for i, e := range languageMarkers {
			if found[i] {
				continue
			}
			found[i] = slices.ContainsFunc(e.markers, func(m string) bool { return bytes.Contains(window, []byte(m)) })
		}
		carry = append(carry[:0], window[max(0, len(window)-(maxLen-1)):]...)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return found, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// jarLanguage names the JVM language of a JAR from its entry names; Java
// when no other language leaves a mark. The application's own classes decide
// Kotlin and Groovy: Java libraries (OkHttp, Spring) bundle their stdlibs in
// nested jars. Scala and Clojure also count by their nested runtime jar, as
// a Java program has no use for those.
func jarLanguage(names []string) string {
	var kotlin, scala, groovy, clojure bool
	for _, name := range names {
		name = strings.TrimPrefix(name, "BOOT-INF/classes/")
		name = strings.TrimPrefix(name, "WEB-INF/classes/")
		switch {
		case strings.HasPrefix(name, "clojure/"):
			clojure = true
		case strings.HasPrefix(name, "scala/"):
			scala = true
		case strings.HasPrefix(name, "groovy/"), strings.HasPrefix(name, "org/codehaus/groovy/"):
			groovy = true
		case strings.HasPrefix(name, "kotlin/"),
			strings.HasPrefix(name, "META-INF/") && strings.HasSuffix(name, ".kotlin_module") && strings.Count(name, "/") == 1:
			kotlin = true
		case isNestedJarName(name):
			base := name[strings.LastIndexByte(name, '/')+1:]
			switch {
			case strings.HasPrefix(base, "scala-library-"), strings.HasPrefix(base, "scala3-library_3-"):
				scala = true
			case strings.HasPrefix(base, "clojure-1."):
				clojure = true
			}
		}
	}
	switch {
	case clojure:
		return LangClojure
	case scala:
		return LangScala
	case groovy:
		return LangGroovy
	case kotlin:
		return LangKotlin
	}
	return LangJava
}

func isNestedJarName(name string) bool {
	return (strings.HasPrefix(name, "BOOT-INF/lib/") || strings.HasPrefix(name, "WEB-INF/lib/")) && strings.HasSuffix(name, ".jar")
}
