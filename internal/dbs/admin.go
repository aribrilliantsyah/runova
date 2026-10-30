package dbs

import (
	"context"
	"log/slog"

	"runova/internal/pg"
	"runova/internal/store"
)

// AdminDBView is a database with its owner, for the admin overview.
type AdminDBView struct {
	DBView
	Owner string `json:"owner"`
}

// ListAll returns every database of every user. Size and connections are 0
// for owners PostgreSQL cannot be asked about.
func (s *Service) ListAll(ctx context.Context) ([]AdminDBView, error) {
	all, err := s.Store.AllDatabases(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.Store.Users(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]store.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	type perUser struct {
		live map[string]pg.DBInfo
		apps map[int64][]string
	}
	cache := map[int64]perUser{}
	out := make([]AdminDBView, 0, len(all))
	for _, d := range all {
		owner := byID[d.UserID]
		pu, ok := cache[owner.ID]
		if !ok {
			pu = perUser{live: map[string]pg.DBInfo{}, apps: map[int64][]string{}}
			if infos, err := s.PG.Databases(ctx, owner.Username); err == nil {
				for _, i := range infos {
					pu.live[i.Name] = i
				}
			} else {
				slog.Warn("list postgres databases", "user", owner.Username, "err", err)
			}
			links, err := s.Store.DatabaseApps(ctx, owner.ID)
			if err != nil {
				return nil, err
			}
			for _, l := range links {
				pu.apps[l.DatabaseID] = append(pu.apps[l.DatabaseID], l.AppName)
			}
			cache[owner.ID] = pu
		}
		v := AdminDBView{Owner: owner.Username, DBView: DBView{ID: d.ID, Name: d.Name, Apps: pu.apps[d.ID], CreatedAt: d.CreatedAt}}
		if v.Apps == nil {
			v.Apps = []string{}
		}
		if i, ok := pu.live[d.Name]; ok {
			v.SizeBytes, v.Connections = i.SizeBytes, i.Connections
		}
		out = append(out, v)
	}
	return out, nil
}
