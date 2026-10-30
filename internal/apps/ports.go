package apps

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"

	"runova/internal/store"
)

const (
	hostPortMin = 30000
	hostPortMax = 39999
)

// netFree reports whether nothing listens on 127.0.0.1:port right now.
func netFree(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// allocHostPort picks a random free host port in 30000–39999 for the
// deployment and records it, so a concurrent allocation cannot pick it too.
// Ports of pending, running and active deployments are taken; the rest are
// probed with PortFree.
func (s *Service) allocHostPort(ctx context.Context, deploymentID int64) (int, error) {
	s.portMu.Lock()
	defer s.portMu.Unlock()
	used, err := s.Store.UsedHostPorts(ctx)
	if err != nil {
		return 0, err
	}
	free := s.PortFree
	if free == nil {
		free = netFree
	}
	const n = hostPortMax - hostPortMin + 1
	start := 0
	if s.randStart != nil {
		start = s.randStart(n)
	} else {
		start = rand.IntN(n)
	}
	for i := range n {
		port := hostPortMin + (start+i)%n
		if used[port] || !free(port) {
			continue
		}
		if err := s.Store.UpdateDeployment(ctx, deploymentID, store.DeploymentPatch{HostPort: &port}); err != nil {
			return 0, err
		}
		return port, nil
	}
	return 0, fmt.Errorf("no free host port in %d–%d", hostPortMin, hostPortMax)
}

// allocIPPort returns the lowest port in the admin's range that no app owns.
func (s *Service) allocIPPort(ctx context.Context, settings store.Settings) (int, error) {
	used, err := s.Store.UsedIPPorts(ctx)
	if err != nil {
		return 0, err
	}
	for p := settings.PortMin; p <= settings.PortMax; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, overQuota("Port akses IP habis")
}
