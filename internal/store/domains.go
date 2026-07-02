package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type Domain struct {
	ID              int64
	Domain          string
	RecordType      string // A / CNAME
	IntervalSeconds int
	Remark          string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func normalizeRecordType(t string) string {
	return strings.ToUpper(strings.TrimSpace(t))
}

func (s *Store) ListDomains(ctx context.Context) ([]Domain, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, domain, record_type, interval_seconds, remark, enabled, created_at, updated_at
		FROM domains
		ORDER BY id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Domain
	for rows.Next() {
		var d Domain
		var enabledInt int
		if err := rows.Scan(&d.ID, &d.Domain, &d.RecordType, &d.IntervalSeconds, &d.Remark, &enabledInt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.Enabled = enabledInt == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CreateDomain(ctx context.Context, domain, recordType string, intervalSeconds int, remark string) (int64, error) {
	recordType = normalizeRecordType(recordType)
	now := time.Now()
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO domains(domain, record_type, interval_seconds, remark, enabled, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?)
	`, strings.TrimSpace(domain), recordType, intervalSeconds, strings.TrimSpace(remark), 1, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteDomain(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, id)
	return err
}

func (s *Store) SetDomainEnabled(ctx context.Context, id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE domains SET enabled = ?, updated_at = ? WHERE id = ?`, v, time.Now(), id)
	return err
}

func (s *Store) GetDomain(ctx context.Context, id int64) (*Domain, error) {
	var d Domain
	var enabledInt int
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, domain, record_type, interval_seconds, remark, enabled, created_at, updated_at
		FROM domains WHERE id = ?
	`, id).Scan(&d.ID, &d.Domain, &d.RecordType, &d.IntervalSeconds, &d.Remark, &enabledInt, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	d.Enabled = enabledInt == 1
	return &d, nil
}

type Check struct {
	ID        int64
	DomainID  int64
	CheckedAt time.Time
	Value     string
	Err       sql.NullString
}

type Change struct {
	ID                     int64
	DomainID               int64
	ChangedAt              time.Time
	OldValue               string
	NewValue               string
	SecondsSinceLastChange int
}

// InsertCheck inserts a check record, and if it detects a value change (successful checks only),
// also inserts a change event. It returns (changed, oldValue, secondsSinceLastChange, error).
func (s *Store) InsertCheck(ctx context.Context, domainID int64, checkedAt time.Time, value string, errStr *string) (bool, string, int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, "", 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// Read previous successful value before inserting current one.
	var prevValue string
	var prevOK bool
	{
		var v string
		e := tx.QueryRowContext(ctx, `
			SELECT value FROM dns_checks
			WHERE domain_id = ? AND err IS NULL
			ORDER BY checked_at DESC
			LIMIT 1
		`, domainID).Scan(&v)
		if e == nil {
			prevValue = v
			prevOK = true
		} else if e != sql.ErrNoRows {
			return false, "", 0, e
		}
	}

	var errNull sql.NullString
	if errStr != nil && strings.TrimSpace(*errStr) != "" {
		errNull = sql.NullString{String: strings.TrimSpace(*errStr), Valid: true}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO dns_checks(domain_id, checked_at, value, err) VALUES(?,?,?,?)
	`, domainID, checkedAt, value, errNull); err != nil {
		return false, "", 0, err
	}

	// Only successful checks participate in change detection.
	if errNull.Valid {
		if err := tx.Commit(); err != nil {
			return false, "", 0, err
		}
		return false, "", 0, nil
	}
	if !prevOK {
		if err := tx.Commit(); err != nil {
			return false, "", 0, err
		}
		return false, "", 0, nil
	}
	if prevValue == value {
		if err := tx.Commit(); err != nil {
			return false, "", 0, err
		}
		return false, "", 0, nil
	}

	// Compute interval since last change (or 0 if none).
	secondsSince := 0
	{
		var lastChangedAt time.Time
		e := tx.QueryRowContext(ctx, `
			SELECT changed_at FROM dns_changes
			WHERE domain_id = ?
			ORDER BY changed_at DESC
			LIMIT 1
		`, domainID).Scan(&lastChangedAt)
		if e == nil {
			secondsSince = int(checkedAt.Sub(lastChangedAt).Seconds())
		} else if e != sql.ErrNoRows {
			return false, "", 0, e
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO dns_changes(domain_id, changed_at, old_value, new_value, seconds_since_last_change)
		VALUES(?,?,?,?,?)
	`, domainID, checkedAt, prevValue, value, secondsSince); err != nil {
		return false, "", 0, err
	}

	if err := tx.Commit(); err != nil {
		return false, "", 0, err
	}
	return true, prevValue, secondsSince, nil
}

func (s *Store) ListChecks(ctx context.Context, domainID int64, limit int) ([]Check, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, domain_id, checked_at, value, err
		FROM dns_checks
		WHERE domain_id = ?
		ORDER BY checked_at DESC
		LIMIT ?
	`, domainID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Check
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.ID, &c.DomainID, &c.CheckedAt, &c.Value, &c.Err); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CountChecks(ctx context.Context, domainID int64) (int, error) {
	var cnt int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM dns_checks WHERE domain_id = ?`, domainID).Scan(&cnt)
	return cnt, err
}

func (s *Store) ListChecksPage(ctx context.Context, domainID int64, page, pageSize int) ([]Check, int, error) {
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 100
	}
	if page <= 0 {
		page = 1
	}
	total, err := s.CountChecks(ctx, domainID)
	if err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, domain_id, checked_at, value, err
		FROM dns_checks
		WHERE domain_id = ?
		ORDER BY checked_at DESC
		LIMIT ? OFFSET ?
	`, domainID, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Check
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.ID, &c.DomainID, &c.CheckedAt, &c.Value, &c.Err); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func (s *Store) ListChanges(ctx context.Context, domainID int64, limit int) ([]Change, error) {
	if limit <= 0 || limit > 5000 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, domain_id, changed_at, old_value, new_value, seconds_since_last_change
		FROM dns_changes
		WHERE domain_id = ?
		ORDER BY changed_at DESC
		LIMIT ?
	`, domainID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.ID, &c.DomainID, &c.ChangedAt, &c.OldValue, &c.NewValue, &c.SecondsSinceLastChange); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type DomainStats struct {
	ChangeCount int
	MinSeconds  int
	MaxSeconds  int
	AvgSeconds  int
}

func (s *Store) GetStats(ctx context.Context, domainID int64) (DomainStats, error) {
	var st DomainStats
	var avg float64
	// SQLite returns NULL when no rows; coalesce to 0.
	err := s.DB.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(MIN(NULLIF(seconds_since_last_change, 0)), 0),
			COALESCE(MAX(seconds_since_last_change), 0),
			COALESCE(AVG(NULLIF(seconds_since_last_change, 0)), 0)
		FROM dns_changes
		WHERE domain_id = ?
	`, domainID).Scan(&st.ChangeCount, &st.MinSeconds, &st.MaxSeconds, &avg)
	st.AvgSeconds = int(avg)
	return st, err
}
