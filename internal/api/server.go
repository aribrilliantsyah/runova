package api

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"runova/internal/store"
)

const healthWait = 3 * time.Second

// Check probes one dependency. It returns a short detail (a version) or the reason it is down.
type Check func(ctx context.Context) (detail string, err error)

// Health holds the probes behind GET /api/server; a nil probe reports "not configured" as down.
type Health struct {
	Docker, Caddy, Postgres Check
}

type depStatus struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *server) probe(ctx context.Context, c Check) depStatus {
	if c == nil {
		return depStatus{Error: "Tidak dikonfigurasi"}
	}
	ctx, cancel := context.WithTimeout(ctx, healthWait)
	defer cancel()
	detail, err := c(ctx)
	if err != nil {
		return depStatus{Error: err.Error()}
	}
	return depStatus{OK: true, Version: detail}
}

type serverStatus struct {
	Docker depStatus `json:"docker"`
	Caddy  struct {
		depStatus
		Domains int `json:"domains"`
	} `json:"caddy"`
	Postgres struct {
		depStatus
		Databases int `json:"databases"`
	} `json:"postgres"`
	Host struct {
		Hostname string `json:"hostname"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		CPUs     int    `json:"cpus"`
		// LoadAvg is /proc/loadavg's 1, 5 and 15 minute run-queue averages; null when unreadable.
		// CPU is shared by every app, so this is the CPU figure, not an allocation.
		LoadAvg     []float64 `json:"load_avg"`
		MemTotalMB  int       `json:"mem_total_mb"`
		MemAllocMB  int       `json:"mem_alloc_mb"`
		DiskTotalMB int       `json:"disk_total_mb"`
		DiskUsedMB  int       `json:"disk_used_mb"`
		// AppDiskAllocMB is the disk allocated to apps; AppDiskUsedBytes what their workspaces held at the last scans.
		AppDiskAllocMB   int   `json:"app_disk_alloc_mb"`
		AppDiskUsedBytes int64 `json:"app_disk_used_bytes"`
		// LocalIP is the machine's LAN address; IPAccess where app IP ports are linked.
		LocalIP  string   `json:"local_ip"`
		IPAccess IPAccess `json:"ip_access"`
	} `json:"host"`
	Apps struct {
		Total   int `json:"total"`
		Running int `json:"running"`
		Stopped int `json:"stopped"`
		Failed  int `json:"failed"`
	} `json:"apps"`
}

// serverStatus reports dependency health, host capacity and what the apps have allocated.
// A dependency that is down is data, not an error: the answer is still 200.
func (s *server) serverStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var out serverStatus

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); out.Docker = s.probe(ctx, s.health.Docker) }()
	go func() { defer wg.Done(); out.Caddy.depStatus = s.probe(ctx, s.health.Caddy) }()
	go func() { defer wg.Done(); out.Postgres.depStatus = s.probe(ctx, s.health.Postgres) }()
	wg.Wait()

	domains, err := s.store.AllDomains(ctx)
	if err != nil {
		serverError(w, r, err)
		return
	}
	out.Caddy.Domains = len(domains)
	dbList, err := s.store.AllDatabases(ctx)
	if err != nil {
		serverError(w, r, err)
		return
	}
	out.Postgres.Databases = len(dbList)

	list, err := s.store.AllApps(ctx)
	if err != nil {
		serverError(w, r, err)
		return
	}
	for _, a := range list {
		out.Apps.Total++
		out.Host.MemAllocMB += a.MemoryMB
		out.Host.AppDiskAllocMB += a.DiskMB
		out.Host.AppDiskUsedBytes += a.DiskUsedBytes
		switch a.Status {
		case store.AppRunning, store.AppStarting, store.AppDeploying:
			out.Apps.Running++
		case store.AppFailed:
			out.Apps.Failed++
		default:
			out.Apps.Stopped++
		}
	}

	if settings, err := s.store.Settings(ctx); err == nil {
		out.Host.IPAccess = ipAccess(settings.PublicIP, interfaceAddrs())
		out.Host.LocalIP = out.Host.IPAccess.LocalIP
	}
	out.Host.Hostname, _ = os.Hostname()
	out.Host.OS = osName()
	out.Host.Arch = runtime.GOOS + "/" + runtime.GOARCH
	out.Host.CPUs = runtime.NumCPU()
	out.Host.MemTotalMB = memTotalMB()
	out.Host.LoadAvg = loadAvg()
	var fs syscall.Statfs_t
	if err := syscall.Statfs(s.dataDir, &fs); err == nil {
		total := int64(fs.Blocks) * int64(fs.Bsize) >> 20
		free := int64(fs.Bfree) * int64(fs.Bsize) >> 20
		out.Host.DiskTotalMB, out.Host.DiskUsedMB = int(total), int(total-free)
	}
	writeJSON(w, http.StatusOK, out)
}

// osName is PRETTY_NAME from os-release, or the OS name when unreadable.
func osName() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return runtime.GOOS
}

// memTotalMB reads MemTotal from /proc/meminfo; 0 when unavailable.
func memTotalMB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "MemTotal:"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				if kb, err := strconv.Atoi(fields[0]); err == nil {
					return kb >> 10
				}
			}
		}
	}
	return 0
}

// loadAvg reads the 1, 5 and 15 minute load averages from /proc/loadavg; nil when unavailable.
func loadAvg() []float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(b))
	if len(fields) < 3 {
		return nil
	}
	out := make([]float64, 3)
	for i := range out {
		if out[i], err = strconv.ParseFloat(fields[i], 64); err != nil {
			return nil
		}
	}
	return out
}

// info is the part of the server settings every signed-in user needs: the
// upload limit for the wizard, the runtimes on offer and the address of the server.
func (s *server) info(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	acc := ipAccess(v.PublicIP, interfaceAddrs())
	writeJSON(w, http.StatusOK, struct {
		UploadLimitMB int             `json:"upload_limit_mb"`
		PublicIP      string          `json:"public_ip"`
		IPAccessHost  string          `json:"ip_access_host"`
		IPAccessLocal bool            `json:"ip_access_local"`
		Runtimes      map[string]bool `json:"runtimes"`
		PanelDomain   string          `json:"panel_domain"`
	}{v.UploadLimitMB, v.PublicIP, acc.Host, acc.Local, v.Runtimes, v.PanelDomain})
}
