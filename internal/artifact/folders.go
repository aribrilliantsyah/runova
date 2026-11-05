package artifact

import (
	"strings"
	"unicode"

	"runova/internal/folders"
)

// DetectedFolder is a folder the app writes to, read from its configuration
// without running it. Source names the file and Key the setting it came from.
type DetectedFolder struct {
	folders.Folder
	Source string `json:"source"`
	Key    string `json:"key"`
}

// folderKeyWords mark a setting whose value is a folder the app writes to.
var folderKeyWords = map[string]bool{
	"STORAGE": true, "UPLOAD": true, "UPLOADS": true, "FILE": true, "FILES": true, "MEDIA": true,
	"ATTACHMENT": true, "ATTACHMENTS": true, "IMAGE": true, "IMAGES": true, "DATA": true,
	"BASEDIR": true, "MULTIPART": true,
}

// folderValueWords mark a path segment of a folder the app writes to.
var folderValueWords = map[string]bool{
	"storage": true, "upload": true, "uploads": true, "files": true, "media": true,
	"attachment": true, "attachments": true, "images": true, "data": true,
}

// notFolderKeyWords mark settings that hold something else: addresses,
// routes, limits, drivers, credentials and database settings.
var notFolderKeyWords = map[string]bool{
	"URL": true, "URI": true, "URLS": true, "ENDPOINT": true, "PREFIX": true, "ROUTE": true, "HOST": true,
	"DRIVER": true, "BUCKET": true, "NAME": true, "DB": true, "DATABASE": true, "DATASOURCE": true, "PGDATA": true,
	"LEVEL": true, "PORT": true, "MODE": true, "TYPE": true, "SIZE": true, "MAX": true, "MIN": true, "LIMIT": true,
	"ENABLED": true, "ENABLE": true, "COUNT": true, "TTL": true, "TIMEOUT": true, "KEY": true, "SECRET": true,
	"TOKEN": true, "PASSWORD": true, "USER": true, "REGION": true, "ACCESS": true, "ID": true, "EXTENSIONS": true,
}

// keyTokens splits a setting name into upper-case words: "app.uploadDir" → APP UPLOAD DIR.
func keyTokens(key string) []string {
	var b strings.Builder
	prevLower := false
	for _, r := range key {
		switch {
		case r == '.' || r == '-' || r == '_':
			b.WriteByte(' ')
			prevLower = false
		case unicode.IsUpper(r):
			if prevLower {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			prevLower = false
		default:
			b.WriteRune(unicode.ToUpper(r))
			prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
		}
	}
	return strings.Fields(b.String())
}

// resolveValue strips quotes, a file: prefix and Spring placeholders: a whole
// ${NAME:default} becomes its default, a leading ${user.dir}/ the working
// directory. Anything else that cannot be known without running the app is
// reported false.
func resolveValue(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	if strings.HasPrefix(v, "${") {
		end := strings.IndexByte(v, '}')
		if end < 0 {
			return "", false
		}
		inner, rest := v[2:end], v[end+1:]
		name, def, hasDef := strings.Cut(inner, ":")
		switch {
		case rest == "" && hasDef:
			v = strings.TrimSpace(def)
		case name == "user.dir" && (rest == "" || strings.HasPrefix(rest, "/")):
			v = "." + rest
		default:
			return "", false
		}
	}
	v = strings.TrimPrefix(v, "file:")
	if v == "" || strings.Contains(v, "${") || strings.Contains(v, ":") {
		return "", false
	}
	return v, true
}

// detectFolder reports whether the setting key=value names a folder the app
// writes to, and which. The key must not hold an address, limit or database
// setting; the folder must be one the app can keep (folders.Parse); and either
// the key or a segment of the path must say it holds files.
func detectFolder(key, value string) (folders.Folder, bool) {
	tokens := keyTokens(key)
	folderKey := false
	for _, t := range tokens {
		if notFolderKeyWords[t] {
			return folders.Folder{}, false
		}
		folderKey = folderKey || folderKeyWords[t]
	}
	v, ok := resolveValue(value)
	if !ok {
		return folders.Folder{}, false
	}
	if !strings.Contains(v, "/") {
		// A bare word is a folder only under a folder key, and not a flag or a number.
		if !folderKey || !unicode.IsLetter(rune(v[0])) {
			return folders.Folder{}, false
		}
		switch strings.ToLower(v) {
		case "true", "false", "yes", "no", "on", "off", "local", "none", "disk", "s3":
			return folders.Folder{}, false
		}
	}
	if strings.Contains(strings.ToLower(v), ".db") {
		return folders.Folder{}, false
	}
	f, err := folders.Parse(v)
	if err != nil {
		return folders.Folder{}, false
	}
	if !folderKey {
		named := false
		for _, seg := range strings.Split(strings.ToLower(f.Path), "/") {
			named = named || folderValueWords[seg]
		}
		if !named {
			return folders.Folder{}, false
		}
	}
	return f, true
}

// folderSet collects detected folders in order; the first source of a path wins.
type folderSet struct{ list []DetectedFolder }

func (s *folderSet) add(key, value, source string) {
	f, ok := detectFolder(key, value)
	if !ok {
		return
	}
	s.addFolder(DetectedFolder{Folder: f, Source: source, Key: key})
}

func (s *folderSet) addFolder(d DetectedFolder) {
	next := append([]DetectedFolder(nil), s.list...)
	taken := map[string]bool{}
	for _, o := range next {
		if o.Path == d.Path {
			return
		}
		taken[o.Dir] = true
	}
	if d.Mount() {
		d.Dir = folders.UniqueDir(d.Dir, taken)
	} else {
		// A relative path has no choice of workspace folder; a mount that took its name moves aside.
		taken[d.Dir] = true
		for i, o := range next {
			if o.Mount() && o.Dir == d.Dir {
				next[i].Dir = folders.UniqueDir(o.Dir, taken)
				taken[next[i].Dir] = true
			}
		}
	}
	next = append(next, d)
	// A path inside or around one already found is the same folder.
	if _, err := folders.Check(folderList(next), ""); err != nil {
		return
	}
	s.list = next
}

func folderList(ds []DetectedFolder) []folders.Folder {
	out := make([]folders.Folder, len(ds))
	for i, d := range ds {
		out[i] = d.Folder
	}
	return out
}

// propertyPairs yields key/value settings of a .properties file.
func propertyPairs(lines []string) [][2]string {
	var out [][2]string
	for _, line := range lines {
		if k, v, ok := splitProperty(line); ok && k != "" {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

// yamlPairs yields the scalar settings of a YAML file as dotted keys; lists
// and multi-line values are skipped.
func yamlPairs(lines []string) [][2]string {
	type level struct {
		indent int
		key    string
	}
	var stack []level
	var out [][2]string
	for _, line := range lines {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		text := strings.TrimSpace(line)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if strings.HasPrefix(text, "-") || text == "---" {
			continue
		}
		key, value, ok := strings.Cut(text, ":")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		value = strings.TrimSpace(value)
		if value == "" {
			stack = append(stack, level{indent, key})
			continue
		}
		parts := make([]string, 0, len(stack)+1)
		for _, l := range stack {
			parts = append(parts, l.key)
		}
		if value == "|" || value == ">" {
			continue
		}
		out = append(out, [2]string{strings.Join(append(parts, key), "."), strings.Trim(value, `"'`)})
	}
	return out
}
