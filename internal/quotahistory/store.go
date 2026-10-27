// Package quotahistory persists independent quota observations for the management panel.
package quotahistory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	_ "modernc.org/sqlite"
)

type Observation struct {
	Credential   string   `json:"credential"`
	Provider     string   `json:"provider"`
	WindowID     string   `json:"windowId"`
	Label        string   `json:"label"`
	ObservedAtMs int64    `json:"observedAtMs"`
	Remaining    *float64 `json:"remaining"`
	ResetAtMs    *int64   `json:"resetAtMs"`
	PeriodHours  *float64 `json:"periodHours"`
}

func open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create quota history directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("create quota history database: %w", err)
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout = 5000;
		CREATE TABLE IF NOT EXISTS observations (
			credential TEXT NOT NULL, provider TEXT NOT NULL,
			window_id TEXT NOT NULL, label TEXT NOT NULL,
			observed_at_ms INTEGER NOT NULL, remaining REAL,
			reset_at_ms INTEGER, period_hours REAL,
			PRIMARY KEY (credential, window_id, observed_at_ms)
		) WITHOUT ROWID;
		CREATE INDEX IF NOT EXISTS observations_time ON observations(observed_at_ms);`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// ReservePoll keeps collection cadence across tabs and panel restarts.
func ReservePoll(ctx context.Context, path, credential string, nowMs, intervalMs int64) (bool, error) {
	db, err := open(path)
	if err != nil {
		return false, err
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Error("close quota history database")
		}
	}()
	if _, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS polls (
		credential TEXT PRIMARY KEY, next_at_ms INTEGER NOT NULL) WITHOUT ROWID`); err != nil {
		return false, err
	}
	result, err := db.ExecContext(ctx, `INSERT INTO polls VALUES (?, ?)
		ON CONFLICT(credential) DO UPDATE SET next_at_ms=excluded.next_at_ms
		WHERE polls.next_at_ms <= ?`, credential, nowMs+intervalMs, nowMs)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

func Record(ctx context.Context, path string, observations []Observation) error {
	db, err := open(path)
	if err != nil {
		return err
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Error("close quota history database")
		}
	}()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if errRollback := tx.Rollback(); errRollback != nil && errRollback != sql.ErrTxDone {
			log.WithError(errRollback).Error("roll back quota history transaction")
		}
	}()
	for _, observation := range observations {
		_, err = tx.ExecContext(ctx, `INSERT INTO observations
			(credential, provider, window_id, label, observed_at_ms, remaining, reset_at_ms, period_hours)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			observation.Credential, observation.Provider, observation.WindowID,
			observation.Label, observation.ObservedAtMs, observation.Remaining,
			observation.ResetAtMs, observation.PeriodHours)
		if err != nil {
			return fmt.Errorf("record quota observation: %w", err)
		}
	}
	return tx.Commit()
}

func Read(ctx context.Context, path string, sinceMs, untilMs int64) ([]Observation, error) {
	db, err := open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Error("close quota history database")
		}
	}()
	// Include one predecessor per series for interpolation at the range boundary.
	rows, err := db.QueryContext(ctx, `SELECT credential, provider, window_id, label,
		observed_at_ms, remaining, reset_at_ms, period_hours FROM observations
		WHERE observed_at_ms BETWEEN ? AND ? OR (credential, window_id, observed_at_ms) IN (
			SELECT credential, window_id, MAX(observed_at_ms) FROM observations
			WHERE observed_at_ms < ? GROUP BY credential, window_id
		) ORDER BY credential, window_id, observed_at_ms`, sinceMs, untilMs, sinceMs)
	if err != nil {
		return nil, err
	}
	defer func() {
		if errClose := rows.Close(); errClose != nil {
			log.WithError(errClose).Error("close quota history rows")
		}
	}()
	observations := []Observation{}
	for rows.Next() {
		var observation Observation
		if err = rows.Scan(&observation.Credential, &observation.Provider,
			&observation.WindowID, &observation.Label, &observation.ObservedAtMs,
			&observation.Remaining, &observation.ResetAtMs, &observation.PeriodHours); err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}
