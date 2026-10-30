package api

import (
	"context"
	"errors"

	"runova/internal/store"
)

type domainBrief struct {
	Host   string             `json:"host"`
	Status store.DomainStatus `json:"status"`
}

// appSummary is what the app list shows beside each app: its domains and its active version.
type appSummary struct {
	Domains    []domainBrief `json:"domains"`
	Version    int           `json:"version"`
	DeployedAt int64         `json:"deployed_at"`
}

// summaries describes the given apps, keyed by app ID.
func (s *server) summaries(ctx context.Context, list []store.App) (map[int64]appSummary, error) {
	out := make(map[int64]appSummary, len(list))
	for _, a := range list {
		out[a.ID] = appSummary{Domains: []domainBrief{}}
	}
	domains, err := s.store.AllDomains(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		if sum, ok := out[d.AppID]; ok {
			sum.Domains = append(sum.Domains, domainBrief{d.Host, d.Status})
			out[d.AppID] = sum
		}
	}
	for _, a := range list {
		if a.CurrentDeploymentID == nil {
			continue
		}
		d, err := s.store.Deployment(ctx, *a.CurrentDeploymentID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sum := out[a.ID]
		sum.Version = d.Number
		sum.DeployedAt = max(d.FinishedAt, d.CreatedAt)
		out[a.ID] = sum
	}
	return out, nil
}
