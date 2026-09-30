package journal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

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

func (s *SQLite) ClaimLease(ctx context.Context, scope, owner string, ttl time.Duration) (loomgit.Lease, error) {
	if scope == "" || owner == "" || ttl <= 0 {
		return loomgit.Lease{}, errors.New("scope, owner and positive TTL are required")
	}
	now := time.Now().UTC()
	var l loomgit.Lease
	var expires int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO journal_leases(scope, owner, fence, expires_at) VALUES (?, ?, 1, ?)
		ON CONFLICT(scope) DO UPDATE SET owner = excluded.owner, fence = journal_leases.fence + 1, expires_at = excluded.expires_at
		WHERE journal_leases.expires_at <= ? RETURNING scope, owner, fence, expires_at`, scope, owner, now.Add(ttl).UnixNano(), now.UnixNano()).Scan(&l.Scope, &l.Owner, &l.Fence, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return l, ErrLeaseHeld
		}
		return l, err
	}
	l.ExpiresAt = time.Unix(0, expires).UTC()
	return l, nil
}
func (s *SQLite) RenewLease(ctx context.Context, prior loomgit.Lease, ttl time.Duration) (loomgit.Lease, error) {
	if ttl <= 0 {
		return loomgit.Lease{}, errors.New("positive TTL is required")
	}
	now := time.Now().UTC()
	var l loomgit.Lease
	var expires int64
	err := s.db.QueryRowContext(ctx, `UPDATE journal_leases SET expires_at = ? WHERE scope = ? AND owner = ? AND fence = ? AND expires_at > ? RETURNING scope, owner, fence, expires_at`, now.Add(ttl).UnixNano(), prior.Scope, prior.Owner, prior.Fence, now.UnixNano()).Scan(&l.Scope, &l.Owner, &l.Fence, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrStale
	}
	if err != nil {
		return l, err
	}
	l.ExpiresAt = time.Unix(0, expires).UTC()
	return l, nil
}
func (s *SQLite) ReleaseLease(ctx context.Context, prior loomgit.Lease) error {
	now := time.Now().UTC()
	r, err := s.db.ExecContext(ctx, `UPDATE journal_leases SET expires_at = 0 WHERE scope = ? AND owner = ? AND fence = ? AND expires_at > ?`, prior.Scope, prior.Owner, prior.Fence, now.UnixNano())
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStale
	}
	return nil
}

// CurrentLease reads a fence without extending or claiming its lease.
func (s *SQLite) CurrentLease(ctx context.Context, scope string) (loomgit.Lease, error) {
	var l loomgit.Lease
	var expires int64
	err := s.db.QueryRowContext(ctx, `SELECT scope,owner,fence,expires_at FROM journal_leases WHERE scope=?`, scope).
		Scan(&l.Scope, &l.Owner, &l.Fence, &expires)
	if err != nil {
		return l, err
	}
	l.ExpiresAt = time.Unix(0, expires).UTC()
	return l, nil
}
