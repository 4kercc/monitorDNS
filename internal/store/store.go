package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

func Open(dbPath string) (*Store, error) {
	// busy_timeout helps reduce "database is locked" in concurrent goroutines.
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_pragma=journal_mode(WAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite works best with a single writer connection in-process.

	s := &Store{DB: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash BLOB NOT NULL,
			created_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token TEXT NOT NULL UNIQUE,
			user_id INTEGER NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL,
			record_type TEXT NOT NULL, -- A / CNAME
			interval_seconds INTEGER NOT NULL,
			remark TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_domains_enabled ON domains(enabled);`,
		`CREATE TABLE IF NOT EXISTS dns_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			checked_at DATETIME NOT NULL,
			value TEXT NOT NULL,
			err TEXT,
			FOREIGN KEY(domain_id) REFERENCES domains(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_checks_domain_time ON dns_checks(domain_id, checked_at DESC);`,
		`CREATE TABLE IF NOT EXISTS dns_changes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			changed_at DATETIME NOT NULL,
			old_value TEXT NOT NULL,
			new_value TEXT NOT NULL,
			seconds_since_last_change INTEGER NOT NULL,
			FOREIGN KEY(domain_id) REFERENCES domains(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_changes_domain_time ON dns_changes(domain_id, changed_at DESC);`,
	}
	for _, stmt := range stmts {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	// Backward-compatible migration: add remark column for existing databases.
	// (SQLite might not support ADD COLUMN IF NOT EXISTS in older versions.)
	hasRemark := false
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(domains)`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull int
			var dflt sql.NullString
			var pk int
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				break
			}
			if name == "remark" {
				hasRemark = true
				break
			}
		}
	}
	if !hasRemark {
		_, _ = s.DB.ExecContext(ctx, `ALTER TABLE domains ADD COLUMN remark TEXT NOT NULL DEFAULT ''`)
	}
	return nil
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

var ErrNotFound = errors.New("not found")

type User struct {
	ID           int64
	Username     string
	PasswordHash []byte
	CreatedAt    time.Time
}
