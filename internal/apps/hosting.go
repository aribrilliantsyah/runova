package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"runova/internal/hostdns"
	"runova/internal/store"
)

// leWindow is how far back created subdomains count towards Let's Encrypt's weekly limit.
const leWindow = 7 * 24 * time.Hour

var (
	// ErrForbidden marks a request the actor's role does not allow.
	ErrForbidden = errors.New("forbidden")
	// ErrHostingNotFound is a hosting domain that does not exist.
	ErrHostingNotFound = &userError{store.ErrNotFound, "Domain hosting tidak ditemukan"}
)

func forbidden(msg string) error { return &userError{ErrForbidden, msg} }

var errHostingUnavailable = invalid("Domain hosting tidak tersedia")

// DomainInput is what a user provides to add an address to an app: either a
// label under a registered hosting domain, or (admins only) a free host name.
type DomainInput struct {
	HostingDomainID int64  `json:"hosting_domain_id"`
	Label           string `json:"label"`
	Host            string `json:"host"`
}

// HostingView is a hosting domain with its usage, for the admin.
type HostingView struct {
	store.HostingDomain
	Used    int     `json:"used"`
	LECount int     `json:"le_count"`
	LELevel LELevel `json:"le_level"`
}

func (s *Service) dnsChecker() interface {
	Check(ctx context.Context, domain, publicIP string) hostdns.Result
} {
	if s.DNS != nil {
		return s.DNS
	}
	return hostdns.Checker{}
}

func requireAdmin(actor store.User) error {
	if !actor.IsAdmin() {
		return forbidden("Hanya admin yang boleh mengelola domain hosting")
	}
	return nil
}

// HostingDomains lists the registered domains: all of them with usage for an
// admin, only the enabled ones for everyone else.
func (s *Service) HostingDomains(ctx context.Context, user store.User) ([]HostingView, error) {
	list, err := s.Store.HostingDomains(ctx)
	if err != nil {
		return nil, err
	}
	if !user.IsAdmin() {
		out := []HostingView{}
		for _, h := range list {
			if h.Enabled {
				out = append(out, HostingView{HostingDomain: h})
			}
		}
		return out, nil
	}
	return s.hostingViews(ctx, list)
}

func (s *Service) hostingViews(ctx context.Context, list []store.HostingDomain) ([]HostingView, error) {
	used, recent, err := s.Store.HostingUsage(ctx, s.now().Add(-leWindow).UnixMilli())
	if err != nil {
		return nil, err
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(list))
	all, err := s.Store.HostingDomains(ctx)
	if err != nil {
		return nil, err
	}
	for _, h := range all {
		names[h.ID] = h.Name
	}
	groups := LEGroups(names, recent, settings.PanelDomain)
	out := make([]HostingView, len(list))
	for i, h := range list {
		out[i] = HostingView{HostingDomain: h, Used: used[h.ID], LECount: groups[h.ID].Count, LELevel: groups[h.ID].Level}
	}
	return out, nil
}

func (s *Service) hostingView(ctx context.Context, h store.HostingDomain) (HostingView, error) {
	v, err := s.hostingViews(ctx, []store.HostingDomain{h})
	if err != nil {
		return HostingView{}, err
	}
	return v[0], nil
}

// AddHostingDomain registers name for hosting and checks its DNS right away.
func (s *Service) AddHostingDomain(ctx context.Context, actor store.User, name, reserved string) (HostingView, error) {
	if err := requireAdmin(actor); err != nil {
		return HostingView{}, err
	}
	name, err := NormalizeHostingName(name)
	if err != nil {
		return HostingView{}, invalid("%s", err.Error())
	}
	extra, err := ParseReserved(reserved)
	if err != nil {
		return HostingView{}, invalid("%s", err.Error())
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return HostingView{}, err
	}
	if name == settings.PanelDomain {
		return HostingView{}, invalid("Domain panel tidak bisa dijadikan domain hosting")
	}
	existing, err := s.Store.HostingDomains(ctx)
	if err != nil {
		return HostingView{}, err
	}
	names := make([]string, len(existing))
	for i, h := range existing {
		names[i] = h.Name
	}
	if other := HostingNesting(name, names); other != "" {
		return HostingView{}, invalid("Bertumpuk dengan domain hosting %s; pakai salah satu saja", other)
	}
	h, err := s.Store.AddHostingDomain(ctx, name, strings.Join(extra, ","), s.now().UnixMilli())
	if errors.Is(err, store.ErrConflict) {
		return HostingView{}, conflict("Domain hosting sudah terdaftar")
	}
	if err != nil {
		return HostingView{}, err
	}
	s.logActivity(ctx, actor, store.ActHostingAdd, name, "")
	return s.recordDNS(ctx, h, settings.PublicIP)
}

func (s *Service) recordDNS(ctx context.Context, h store.HostingDomain, publicIP string) (HostingView, error) {
	res := s.dnsChecker().Check(ctx, h.Name, publicIP)
	status := store.DNSOK
	if !res.OK {
		status = store.DNSError
	}
	if err := s.Store.SetHostingDNS(ctx, h.ID, status, res.Detail, s.now().UnixMilli()); err != nil {
		return HostingView{}, err
	}
	h, err := s.Store.HostingDomain(ctx, h.ID)
	if err != nil {
		return HostingView{}, err
	}
	return s.hostingView(ctx, h)
}

// CheckHostingDNS checks the domain's DNS now and records the result.
func (s *Service) CheckHostingDNS(ctx context.Context, actor store.User, id int64) (HostingView, error) {
	if err := requireAdmin(actor); err != nil {
		return HostingView{}, err
	}
	h, err := s.hosting(ctx, id)
	if err != nil {
		return HostingView{}, err
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return HostingView{}, err
	}
	return s.recordDNS(ctx, h, settings.PublicIP)
}

func (s *Service) hosting(ctx context.Context, id int64) (store.HostingDomain, error) {
	h, err := s.Store.HostingDomain(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.HostingDomain{}, ErrHostingNotFound
	}
	return h, err
}

// UpdateHostingDomain enables or disables the domain and edits its extra
// reserved labels. Disabling removes every subdomain's route from Caddy and
// notifies the owners of the apps that lose an address; if Caddy refuses a
// change the previous state is restored.
func (s *Service) UpdateHostingDomain(ctx context.Context, actor store.User, id int64, enabled *bool, reserved *string) (HostingView, error) {
	if err := requireAdmin(actor); err != nil {
		return HostingView{}, err
	}
	old, err := s.hosting(ctx, id)
	if err != nil {
		return HostingView{}, err
	}
	var normalized *string
	if reserved != nil {
		extra, err := ParseReserved(*reserved)
		if err != nil {
			return HostingView{}, invalid("%s", err.Error())
		}
		joined := strings.Join(extra, ",")
		normalized = &joined
	}
	toggled := enabled != nil && *enabled != old.Enabled
	h, err := s.Store.UpdateHostingDomain(ctx, id, enabled, normalized)
	if err != nil {
		return HostingView{}, err
	}
	if toggled {
		if err := s.ApplyCaddy(ctx); err != nil {
			if _, rerr := s.Store.UpdateHostingDomain(context.WithoutCancel(ctx), id, &old.Enabled, nil); rerr != nil {
				slog.Error("restore hosting domain", "name", old.Name, "err", rerr)
			}
			return HostingView{}, err
		}
		if !h.Enabled {
			s.notifyHostingOff(ctx, h)
		}
	}
	detail := "pengaturan"
	switch {
	case toggled && h.Enabled:
		detail = "dinyalakan"
	case toggled:
		detail = "dimatikan"
	}
	s.logActivity(ctx, actor, store.ActHostingUpdate, h.Name, detail)
	return s.hostingView(ctx, h)
}

// notifyHostingOff tells the owner of each app that had a subdomain of h.
func (s *Service) notifyHostingOff(ctx context.Context, h store.HostingDomain) {
	subs, err := s.Store.HostedDomains(ctx, h.ID)
	if err != nil {
		slog.Error("list subdomains", "name", h.Name, "err", err)
		return
	}
	seen := map[int64]bool{}
	for _, d := range subs {
		if seen[d.AppID] {
			continue
		}
		seen[d.AppID] = true
		app, err := s.Store.App(ctx, d.AppID)
		if err != nil {
			slog.Error("notify hosting off", "app", d.AppID, "err", err)
			continue
		}
		s.notify(ctx, app, store.LevelWarning, "Domain hosting "+h.Name+" dimatikan admin",
			"Alamat aplikasi di bawah domain ini tidak lagi dilayani. Pindahkan ke domain hosting lain.")
	}
}

// RemoveHostingDomain deletes a domain no subdomain uses any more.
func (s *Service) RemoveHostingDomain(ctx context.Context, actor store.User, id int64) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	h, err := s.hosting(ctx, id)
	if err != nil {
		return err
	}
	err = s.Store.RemoveHostingDomain(ctx, id)
	var inUse *store.HostingInUseError
	if errors.As(err, &inUse) {
		return conflict(fmt.Sprintf("Masih dipakai %d subdomain; matikan dulu", inUse.N))
	}
	if errors.Is(err, store.ErrNotFound) {
		return ErrHostingNotFound
	}
	if err != nil {
		return err
	}
	s.logActivity(ctx, actor, store.ActHostingRemove, h.Name, "")
	return nil
}

// SubdomainQuota is how many hosting subdomains the user has of the number
// their app quota allows.
func (s *Service) SubdomainQuota(ctx context.Context, user store.User) (used, limit int, err error) {
	used, err = s.Store.SubdomainCount(ctx, user.ID)
	return used, user.Quota.Apps, err
}

// labelProblem returns why label cannot be taken on h, or "" when it can.
func (s *Service) labelProblem(ctx context.Context, h store.HostingDomain, label string) (string, error) {
	if err := ValidateLabel(label); err != nil {
		return err.Error(), nil
	}
	settings, err := s.Store.Settings(ctx)
	if err != nil {
		return "", err
	}
	extra, _ := ParseReserved(h.Reserved)
	if slices.Contains(ReservedLabels(extra, settings.PanelDomain, h.Name), label) {
		return fmt.Sprintf("Nama \"%s\" dicadangkan dan tidak bisa dipakai", label), nil
	}
	host := label + "." + h.Name
	taken, err := s.Store.DomainHostTaken(ctx, host)
	if err != nil {
		return "", err
	}
	if taken || host == settings.PanelDomain {
		return "Alamat sudah dipakai", nil
	}
	return "", nil
}

// HostingAvailable says whether label can be taken on the hosting domain,
// without storing anything; reason explains a no.
func (s *Service) HostingAvailable(ctx context.Context, id int64, label string) (ok bool, reason string, err error) {
	h, err := s.Store.HostingDomain(ctx, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && !h.Enabled {
		return false, errHostingUnavailable.Error(), nil
	}
	if err != nil {
		return false, "", err
	}
	reason, err = s.labelProblem(ctx, h, strings.ToLower(strings.TrimSpace(label)))
	return reason == "", reason, err
}

// addHostedDomain creates label.<hosting domain> for the app within the
// owner's subdomain quota.
func (s *Service) addHostedDomain(ctx context.Context, app store.App, owner store.User, in DomainInput) (store.Domain, error) {
	h, err := s.Store.HostingDomain(ctx, in.HostingDomainID)
	if errors.Is(err, store.ErrNotFound) || err == nil && !h.Enabled {
		return store.Domain{}, errHostingUnavailable
	}
	if err != nil {
		return store.Domain{}, err
	}
	label := strings.ToLower(strings.TrimSpace(in.Label))
	problem, err := s.labelProblem(ctx, h, label)
	if err != nil {
		return store.Domain{}, err
	}
	if problem == "Alamat sudah dipakai" {
		return store.Domain{}, conflict(problem)
	}
	if problem != "" {
		return store.Domain{}, invalid("%s", problem)
	}
	d, err := s.Store.AddHostedDomain(ctx, app.ID, h.ID, label, label+"."+h.Name, owner.Quota.Apps, s.now().UnixMilli())
	var q *store.SubdomainQuotaError
	switch {
	case errors.As(err, &q):
		return store.Domain{}, overQuota("Kuota subdomain habis (%d dari %d)", q.Used, q.Limit)
	case errors.Is(err, store.ErrHostingUnavailable):
		return store.Domain{}, errHostingUnavailable
	}
	return d, err
}
