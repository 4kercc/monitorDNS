package store

import (
	"context"
	"database/sql"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// EnsureAutoAdminUser creates the first user when the system boots for the first time.
// It returns (username, plainPassword, created, error).
func (s *Store) EnsureAutoAdminUser(ctx context.Context) (string, string, bool, error) {
	var cnt int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&cnt); err != nil {
		return "", "", false, err
	}
	if cnt > 0 {
		return "", "", false, nil
	}

	username := "admin"
	plain, err := randomToken(18) // ~24 chars
	if err != nil {
		return "", "", false, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", "", false, err
	}

	now := time.Now()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)`, username, hash, now)
	if err != nil {
		return "", "", false, err
	}
	return username, plain, true, nil
}

func (s *Store) Authenticate(ctx context.Context, username, password string) (*User, error) {
	u := &User{}
	err := s.DB.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword(u.PasswordHash, []byte(password)); err != nil {
		return nil, ErrNotFound
	}
	return u, nil
}

func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, time.Time, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now()
	expires := now.Add(ttl)
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO sessions(token, user_id, expires_at, created_at) VALUES(?,?,?,?)`,
		token, userID, expires, now,
	)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Store) GetUserBySession(ctx context.Context, token string) (*User, error) {
	u := &User{}
	err := s.DB.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.password_hash, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?
	`, token, time.Now()).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

