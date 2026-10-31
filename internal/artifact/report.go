// Package artifact inspects uploaded files (JAR, ELF binary, tar.gz) without
// executing them and reports what Runova needs to know to deploy them.
package artifact

import (
	"regexp"
	"slices"
	"strings"
)

type Kind string

const (
	KindJar    Kind = "jar"
	KindStatic Kind = "static"
	KindGlibc  Kind = "glibc"
)

// Sources of an EnvVar, from most to least authoritative.
const (
	SourceEnvExample = "env.example"
	SourceProperties = "properties"
	SourceScan       = "scan"
)

const (
	defaultPort       = 8080
	portSourceDefault = "bawaan"
)

type EnvVar struct {
	Name    string `json:"name"`
	Default string `json:"default"`
	Secret  bool   `json:"secret"`
	Source  string `json:"source"`
}

type Report struct {
	Kind       Kind     `json:"kind"`
	FileName   string   `json:"file_name"`
	Size       int64    `json:"size"`
	SHA256     string   `json:"sha256"`
	MinJava    int      `json:"min_java"`
	MainClass  string   `json:"main_class"`
	Port       int      `json:"port"`
	PortSource string   `json:"port_source"`
	Env        []EnvVar `json:"env"`
	Problems   []string `json:"problems"`
	// Language is the programming language the artifact was written in
	// (the Lang* constants); "" when it could not be told.
	Language string `json:"language"`
}

// OK reports whether the artifact has no problem blocking a deploy.
func (r Report) OK() bool { return len(r.Problems) == 0 }

// newReport returns a Report whose slices marshal as arrays, not null.
func newReport(kind Kind) Report {
	return Report{
		Kind:       kind,
		Port:       defaultPort,
		PortSource: portSourceDefault,
		Env:        []EnvVar{},
		Problems:   []string{},
	}
}

// problemReport is a Report that only carries a blocking problem.
func problemReport(msg string) Report {
	r := newReport("")
	r.Port, r.PortSource = 0, ""
	r.Problems = append(r.Problems, msg)
	return r
}

var envNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

var secretWords = []string{"PASSWORD", "SECRET", "TOKEN", "KEY"}

func validEnvName(name string) bool { return envNameRE.MatchString(name) }

func secretName(name string) bool {
	return slices.ContainsFunc(secretWords, func(w string) bool { return strings.Contains(name, w) })
}

// envSet collects env vars by name; the first source to add a name wins, but
// Secret is OR-ed across all additions.
type envSet map[string]*EnvVar

func (s envSet) add(v EnvVar) {
	v.Secret = v.Secret || secretName(v.Name)
	if cur, ok := s[v.Name]; ok {
		cur.Secret = cur.Secret || v.Secret
		return
	}
	s[v.Name] = &v
}

func (s envSet) list() []EnvVar {
	out := make([]EnvVar, 0, len(s))
	for _, v := range s {
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b EnvVar) int { return strings.Compare(a.Name, b.Name) })
	return out
}
