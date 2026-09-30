package journal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
)

// ProxyKey is a per-host secret persisted beside the attempt leases.
func (s *SQLite) ProxyKey(ctx context.Context) ([]byte, error) {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS proxy_key (id INTEGER PRIMARY KEY CHECK(id=1), secret BLOB NOT NULL)`); err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO proxy_key(id,secret) VALUES(1,?) ON CONFLICT(id) DO NOTHING`, secret); err != nil {
		return nil, err
	}
	var stored []byte
	err := s.db.QueryRowContext(ctx, `SELECT secret FROM proxy_key WHERE id=1`).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("proxy key missing")
	}
	return stored, err
}
