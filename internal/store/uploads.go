package store

import (
	"context"
	"encoding/json"
	"fmt"

	"runova/internal/artifact"
)

// Upload is a temporary uploaded artifact awaiting deploy.
type Upload struct {
	ID           string
	UserID       int64
	Dir          string
	ArtifactPath string
	Report       artifact.Report
	CreatedAt    int64
}

const uploadCols = `id, user_id, dir, artifact_path, report, created_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanUpload(row rowScanner) (Upload, error) {
	var (
		u   Upload
		raw string
	)
	if err := row.Scan(&u.ID, &u.UserID, &u.Dir, &u.ArtifactPath, &raw, &u.CreatedAt); err != nil {
		return Upload{}, err
	}
	if err := json.Unmarshal([]byte(raw), &u.Report); err != nil {
		return Upload{}, fmt.Errorf("decode upload report: %w", err)
	}
	return u, nil
}

func (s *Store) CreateUpload(ctx context.Context, u Upload) error {
	raw, err := json.Marshal(u.Report)
	if err != nil {
		return fmt.Errorf("encode upload report: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO uploads(`+uploadCols+`) VALUES(?,?,?,?,?,?)`,
		u.ID, u.UserID, u.Dir, u.ArtifactPath, string(raw), u.CreatedAt); err != nil {
		return fmt.Errorf("create upload: %w", err)
	}
	return nil
}

// Upload returns an upload owned by userID; another user's upload is ErrNotFound.
func (s *Store) Upload(ctx context.Context, id string, userID int64) (Upload, error) {
	u, err := scanUpload(s.db.QueryRowContext(ctx,
		`SELECT `+uploadCols+` FROM uploads WHERE id = ? AND user_id = ?`, id, userID))
	return u, notFound(err, "upload")
}

// CountUploads returns how many uploads the user has waiting.
func (s *Store) CountUploads(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM uploads WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count uploads: %w", err)
	}
	return n, nil
}

func (s *Store) DeleteUpload(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete upload: %w", err)
	}
	return nil
}

// ExpiredUploads lists uploads created before the given time (Unix ms).
func (s *Store) ExpiredUploads(ctx context.Context, before int64) ([]Upload, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+uploadCols+` FROM uploads WHERE created_at < ? ORDER BY created_at`, before)
	if err != nil {
		return nil, fmt.Errorf("list expired uploads: %w", err)
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, fmt.Errorf("list expired uploads: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list expired uploads: %w", err)
	}
	return out, nil
}
