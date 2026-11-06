package apps

import (
	"context"
	"log/slog"
)

// LiftCPULimits removes the per-app CPU cap that Runova 0.3.12 and earlier set
// on every app container (NanoCpus), so apps created before CPU became shared
// use all cores at once instead of only after their next deploy. It is safe to
// run on every start: the update is idempotent, leaves the container running
// and touches only containers labelled as Runova apps. Failures are logged and
// skipped; such a container loses its cap at its next deploy.
func (s *Service) LiftCPULimits(ctx context.Context) {
	list, err := s.Engine.ListContainers(ctx, map[string]string{appLabel: ""})
	if err != nil {
		slog.Warn("lift cpu limits: list containers", "err", err)
		return
	}
	for _, c := range list {
		if err := s.Engine.ClearCPULimit(ctx, c.ID); err != nil {
			slog.Warn("lift cpu limit", "container", c.ID, "err", err)
		}
	}
}
