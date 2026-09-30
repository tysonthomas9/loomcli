package journal

import (
	"database/sql"
	"net/url"
	"path/filepath"
)

// OpenSQLiteReadOnly opens an existing journal without running schema migrations.
func OpenSQLiteReadOnly(path string) (*SQLite, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	query := u.Query()
	query.Set("mode", "ro")
	query.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLite{db: db}, nil
}
