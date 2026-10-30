package artifact

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	maxNestedJar    = 64 << 20
	maxManifest     = 1 << 20
	maxPropsFile    = 1 << 20
	manifestPath    = "META-INF/MANIFEST.MF"
	multiReleaseDir = "META-INF/versions/"
	maxJavaKnown    = 25
	classMagic      = 0xCAFEBABE
)

var propertyFiles = []string{
	"BOOT-INF/classes/application.properties",
	"application.properties",
	"BOOT-INF/classes/application.yml",
	"application.yml",
	"BOOT-INF/classes/application.yaml",
	"application.yaml",
}

var placeholderRE = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]{1,63})(?::([^}]*))?\}`)

// InspectJar reports on a JAR file. The error is only for I/O failures;
// malformed content is described in Report.Problems.
func InspectJar(path string) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Report{}, err
	}
	rep := newReport(KindJar)
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		rep.Problems = append(rep.Problems, "Bukan file JAR yang valid")
		return rep, nil
	}
	if err := inspectZip(zr, &rep); err != nil {
		rep = newReport(KindJar)
		rep.Problems = append(rep.Problems, "Bukan file JAR yang valid")
	}
	return rep, nil
}

func inspectZip(zr *zip.Reader, rep *Report) error {
	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byName[f.Name] = f
	}

	mainClass, err := manifestMainClass(byName[manifestPath])
	if err != nil {
		return err
	}
	rep.MainClass = mainClass
	if mainClass == "" {
		rep.Problems = append(rep.Problems, "Manifest tidak punya Main-Class")
	}

	major, err := maxClassMajor(zr, true)
	if err != nil {
		return err
	}
	switch {
	case major == 0:
		rep.Problems = append(rep.Problems, "JAR tidak berisi kelas Java")
	default:
		rep.MinJava = minJava(major)
		if rep.MinJava > maxJavaKnown {
			rep.Problems = append(rep.Problems, fmt.Sprintf("Butuh Java lebih baru dari %d", maxJavaKnown))
		}
	}

	env := envSet{}
	for _, name := range propertyFiles {
		f := byName[name]
		if f == nil {
			continue
		}
		data, err := readEntry(f, maxPropsFile)
		if err != nil {
			return err
		}
		yaml := !strings.HasSuffix(name, ".properties")
		if rep.PortSource == portSourceDefault {
			if port := configPort(string(data), yaml); port != 0 {
				rep.Port, rep.PortSource = port, name[strings.LastIndexByte(name, '/')+1:]
			}
		}
		for _, n := range placeholders(string(data), yaml) {
			env.add(EnvVar{Name: n, Source: SourceProperties})
		}
	}
	rep.Env = env.list()
	return nil
}

// minJava maps a class file major version to the lowest LTS release that runs it.
func minJava(major uint16) int {
	switch {
	case major <= 52:
		return 8
	case major <= 55:
		return 11
	case major <= 61:
		return 17
	case major <= 65:
		return 21
	case major <= 69:
		return 25
	default:
		return int(major) - 44
	}
}

func readEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}

// manifestMainClass returns Start-Class (Spring Boot) or Main-Class from the
// main section of the manifest; "" when absent.
func manifestMainClass(f *zip.File) (string, error) {
	if f == nil {
		return "", nil
	}
	data, err := readEntry(f, maxManifest)
	if err != nil {
		return "", err
	}
	var attrs []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		if line[0] == ' ' && len(attrs) > 0 {
			attrs[len(attrs)-1] += line[1:]
			continue
		}
		attrs = append(attrs, line)
	}
	var mainClass, startClass string
	for _, a := range attrs {
		name, value, ok := strings.Cut(a, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(name) {
		case "main-class":
			mainClass = strings.TrimSpace(value)
		case "start-class":
			startClass = strings.TrimSpace(value)
		}
	}
	if startClass != "" {
		return startClass, nil
	}
	return mainClass, nil
}

// maxClassMajor returns the highest class file major version in zr, ignoring
// multi-release entries. With nested true it also opens BOOT-INF/lib and
// WEB-INF/lib jars from memory. 0 means no class was found.
func maxClassMajor(zr *zip.Reader, nested bool) (uint16, error) {
	var highest uint16
	for _, f := range zr.File {
		switch {
		case f.FileInfo().IsDir() || strings.HasPrefix(f.Name, multiReleaseDir):
		case strings.HasSuffix(f.Name, ".class"):
			m, err := classMajor(f)
			if err != nil {
				return 0, err
			}
			highest = max(highest, m)
		case nested && isNestedJar(f.Name) && f.UncompressedSize64 <= maxNestedJar:
			data, err := readEntry(f, maxNestedJar)
			if err != nil {
				return 0, err
			}
			inner, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				return 0, err
			}
			m, err := maxClassMajor(inner, false)
			if err != nil {
				return 0, err
			}
			highest = max(highest, m)
		}
	}
	return highest, nil
}

func isNestedJar(name string) bool {
	dir, base := path.Split(name)
	return strings.HasSuffix(base, ".jar") && (dir == "BOOT-INF/lib/" || dir == "WEB-INF/lib/")
}

// classMajor reads the major version from a class file header; entries that
// are not class files report 0.
func classMajor(f *zip.File) (uint16, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	var hdr [8]byte
	if _, err := io.ReadFull(rc, hdr[:]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return 0, nil
		}
		return 0, err
	}
	if binary.BigEndian.Uint32(hdr[:4]) != classMagic {
		return 0, nil
	}
	return binary.BigEndian.Uint16(hdr[6:]), nil
}

// configLines yields the meaningful lines of a properties or YAML file:
// full-line comments and (for YAML) trailing " #" comments are removed.
func configLines(text string, yaml bool) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(nil, maxPropsFile)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r \t")
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || trimmed[0] == '#' || (!yaml && trimmed[0] == '!') {
			continue
		}
		if yaml {
			if i := strings.Index(line, " #"); i >= 0 {
				line = strings.TrimRight(line[:i], " \t")
			}
		}
		out = append(out, line)
	}
	return out
}

func placeholders(text string, yaml bool) []string {
	var names []string
	for _, line := range configLines(text, yaml) {
		for _, m := range placeholderRE.FindAllStringSubmatch(line, -1) {
			names = append(names, m[1])
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// configPort extracts server.port; 0 when absent or not resolvable to a
// number (a placeholder without numeric default has no known value).
func configPort(text string, yaml bool) int {
	lines := configLines(text, yaml)
	if yaml {
		return portFromValue(yamlServerPort(lines))
	}
	for _, line := range lines {
		key, value, ok := splitProperty(line)
		if ok && key == "server.port" {
			return portFromValue(value)
		}
	}
	return 0
}

func splitProperty(line string) (key, value string, ok bool) {
	line = strings.TrimLeft(line, " \t")
	i := strings.IndexAny(line, "=: \t")
	if i < 0 {
		return "", "", false
	}
	key = line[:i]
	rest := strings.TrimLeft(line[i:], " \t")
	if rest != "" && (rest[0] == '=' || rest[0] == ':') {
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	return key, rest, true
}

// yamlServerPort finds `port:` directly under a top-level `server:` key.
func yamlServerPort(lines []string) string {
	inServer := false
	childIndent := -1
	for _, line := range lines {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		text := line[indent:]
		if indent == 0 {
			inServer = text == "server:"
			childIndent = -1
			continue
		}
		if !inServer {
			continue
		}
		if childIndent < 0 {
			childIndent = indent
		}
		if indent != childIndent {
			continue
		}
		if v, ok := strings.CutPrefix(text, "port:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func portFromValue(v string) int {
	v = strings.TrimSpace(v)
	if m := placeholderRE.FindStringSubmatch(v); m != nil && v == m[0] {
		v = m[2]
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}
