package apps

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/publicsuffix"

	"runova/internal/caddy"
)

var (
	labelChars = regexp.MustCompile(`^[a-z0-9-]+$`)
	labelRE    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$`)

	// defaultReserved are labels no user may take on any hosting domain.
	defaultReserved = []string{"www", "api", "mail", "smtp", "ftp", "admin", "panel", "runova", "ns1", "ns2", "localhost"}
)

// ValidateLabel checks the single label a user puts before a hosting domain.
func ValidateLabel(label string) error {
	switch {
	case len(label) < 3 || len(label) > 40:
		return errors.New("Nama alamat harus 3–40 karakter")
	case !labelChars.MatchString(label):
		return errors.New("Nama alamat hanya boleh huruf kecil, angka, dan tanda hubung")
	case strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-"):
		return errors.New("Nama alamat tidak boleh diawali atau diakhiri tanda hubung")
	case len(label) > 3 && label[2] == '-' && label[3] == '-':
		return errors.New("Nama alamat tidak boleh memakai -- di posisi ketiga dan keempat")
	case !labelRE.MatchString(label):
		return errors.New("Nama alamat tidak valid")
	}
	return nil
}

// ParseReserved parses the comma separated extra reserved labels of a hosting
// domain: lowercase, trimmed, each a valid label, without duplicates.
func ParseReserved(s string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if err := ValidateLabel(part); err != nil {
			return nil, errors.New("Nama cadangan \"" + part + "\": " + err.Error())
		}
		if !slices.Contains(out, part) {
			out = append(out, part)
		}
	}
	return out, nil
}

// ReservedLabels is everything a user may not take on the hosting domain:
// the defaults, the domain's own extras, and the panel's leftmost label when
// the panel domain sits exactly one label under the hosting domain.
func ReservedLabels(extra []string, panelDomain, hostingName string) []string {
	out := append(slices.Clone(defaultReserved), extra...)
	if label, ok := strings.CutSuffix(panelDomain, "."+hostingName); ok && label != "" && !strings.Contains(label, ".") {
		out = append(out, label)
	}
	return out
}

// NormalizeHostingName turns what the admin typed ("*.Porto.example.id.") into
// the registered name.
func NormalizeHostingName(s string) (string, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "*.")
	return caddy.ValidateDomain(s)
}

// HostingNesting returns the registered domain that name lies under or
// encloses, or "" when none does: two nested registrations would give one host
// two spellings.
func HostingNesting(name string, existing []string) string {
	for _, e := range existing {
		if e != name && (strings.HasSuffix(name, "."+e) || strings.HasSuffix(e, "."+name)) {
			return e
		}
	}
	return ""
}

// LELevel grades how close a registrable domain is to Let's Encrypt's limit
// of 50 new certificates per week.
type LELevel string

const (
	LEOK   LELevel = ""
	LEWarn LELevel = "warn"
	LECrit LELevel = "crit"

	leWarnAt = 40
	leCritAt = 50
)

// LEUsage is the estimated number of certificates issued in the last week for
// the registrable domain of a hosting domain.
type LEUsage struct {
	Count int
	Level LELevel
}

// LEGroups sums, per hosting domain, the subdomains created in the window
// (recent, by hosting domain ID) over every hosting domain sharing its
// registrable domain (eTLD+1), plus the panel's own certificate when the panel
// domain belongs to that group. It is an estimate, not Let's Encrypt's count.
func LEGroups(domains map[int64]string, recent map[int64]int, panelDomain string) map[int64]LEUsage {
	root := func(name string) string {
		if r, err := publicsuffix.EffectiveTLDPlusOne(name); err == nil {
			return r
		}
		return name
	}
	total := map[string]int{}
	for id, name := range domains {
		total[root(name)] += recent[id]
	}
	if panelDomain != "" {
		if r := root(panelDomain); hasKey(total, r) {
			total[r]++
		}
	}
	out := make(map[int64]LEUsage, len(domains))
	for id, name := range domains {
		n := total[root(name)]
		level := LEOK
		switch {
		case n >= leCritAt:
			level = LECrit
		case n >= leWarnAt:
			level = LEWarn
		}
		out[id] = LEUsage{Count: n, Level: level}
	}
	return out
}

func hasKey(m map[string]int, k string) bool { _, ok := m[k]; return ok }
