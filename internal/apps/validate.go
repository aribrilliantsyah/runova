package apps

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"runova/internal/store"
)

var (
	// ErrQuota marks a request refused because a quota or port range is used up.
	ErrQuota = errors.New("quota exceeded")
	// ErrValidation marks a request the user has to correct.
	ErrValidation = errors.New("validation failed")
)

// userError carries a message meant for the user and the sentinel it matches.
type userError struct {
	kind error
	msg  string
}

func (e *userError) Error() string { return e.msg }
func (e *userError) Unwrap() error { return e.kind }

func invalid(format string, a ...any) error {
	return &userError{ErrValidation, fmt.Sprintf(format, a...)}
}

func overQuota(format string, a ...any) error {
	return &userError{ErrQuota, fmt.Sprintf(format, a...)}
}

func conflict(msg string) error { return &userError{store.ErrConflict, msg} }

// Usage is what a user's apps were given, and the disk their workspaces used at the last scans.
type Usage struct {
	Apps, MemoryMB, DiskMB int
	DiskUsedBytes          int64
}

const (
	minMemoryMB   = 128
	memoryStepMB  = 64
	maxEnvEntries = 200
	maxEnvValue   = 32 << 10
	maxJVMOpts    = 1 << 10
)

// JavaVersions lists the supported JRE major versions.
var JavaVersions = []int{8, 11, 17, 21, 25}

var (
	appNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,38}[a-z0-9]$`)
	envNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)
)

func ValidateAppName(name string) error {
	if !appNameRE.MatchString(name) {
		return invalid("Nama aplikasi harus 3–40 karakter: huruf kecil, angka, dan tanda hubung; diawali huruf dan tidak diakhiri tanda hubung")
	}
	return nil
}

// validateMemory checks the memory is within the allowed steps, ignoring quota.
func validateMemory(memMB int) error {
	if memMB < minMemoryMB || memMB%memoryStepMB != 0 {
		return invalid("Memori minimal %d MB dan kelipatan %d MB", minMemoryMB, memoryStepMB)
	}
	return nil
}

func checkMemory(q store.Quota, used Usage, memMB int) error {
	if left := max(q.MemoryMB-used.MemoryMB, 0); memMB > left {
		return overQuota("Memori melebihi sisa kuota (sisa %d MB)", left)
	}
	return nil
}

// ValidateResources checks a new app's memory against the quota that is left
// after used. A bad value is ErrValidation; too much is ErrQuota.
func ValidateResources(q store.Quota, used Usage, memMB int) error {
	if err := validateMemory(memMB); err != nil {
		return err
	}
	return checkMemory(q, used, memMB)
}

// ValidateJVMOpts accepts space-separated tokens that each start with "-".
func ValidateJVMOpts(opts string) error {
	if len(opts) > maxJVMOpts {
		return invalid("Opsi JVM maksimal %d byte", maxJVMOpts)
	}
	for _, tok := range strings.Fields(opts) {
		if !strings.HasPrefix(tok, "-") {
			return invalid("Opsi JVM harus diawali \"-\": %s", tok)
		}
	}
	return nil
}

// ValidateEnv checks names, duplicates, count and value size.
func ValidateEnv(env []store.EnvEntry) error {
	if len(env) > maxEnvEntries {
		return invalid("Maksimal %d variabel lingkungan", maxEnvEntries)
	}
	seen := make(map[string]bool, len(env))
	for _, e := range env {
		switch {
		case !envNameRE.MatchString(e.Name):
			return invalid("Nama variabel %q tidak valid: huruf besar, angka, dan garis bawah, diawali huruf", e.Name)
		case seen[e.Name]:
			return invalid("Variabel %s muncul lebih dari sekali", e.Name)
		case len(e.Value) > maxEnvValue:
			return invalid("Nilai %s melebihi %d KB", e.Name, maxEnvValue>>10)
		case strings.ContainsRune(e.Value, 0):
			return invalid("Nilai %s tidak boleh mengandung karakter NUL", e.Name)
		}
		seen[e.Name] = true
	}
	return nil
}

func validateInternalPort(p int) error {
	if p < 1 || p > 65535 {
		return invalid("Port internal harus antara 1 dan 65535")
	}
	return nil
}

// validateFileName rejects names that cannot be placed safely on disk or in a bind mount.
func validateFileName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:") ||
		strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return invalid("Nama file artefak tidak valid")
	}
	return nil
}

// allowedJava lists the Java versions the admin allows that are at least min.
func allowedJava(runtimes map[string]bool, min int) []int {
	var out []int
	for _, v := range JavaVersions {
		if v >= min && runtimes["java"+strconv.Itoa(v)] {
			out = append(out, v)
		}
	}
	return out
}

// validateJava checks a chosen Java version against the artifact and the admin's runtimes.
func validateJava(runtimes map[string]bool, minJava, v int) error {
	switch {
	case !slices.Contains(JavaVersions, v):
		return invalid("Versi Java tidak dikenal: %d", v)
	case v < minJava:
		return invalid("Aplikasi ini butuh Java %d atau lebih baru", minJava)
	case !runtimes["java"+strconv.Itoa(v)]:
		return invalid("Java %d dinonaktifkan admin di server ini", v)
	}
	return nil
}
