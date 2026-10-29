package quotahistory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	log "github.com/sirupsen/logrus"
)

// CredentialID identifies a history series independently of its installation directory.
func CredentialID(provider, relativeFile string) string {
	sum := sha256.Sum256([]byte(provider + ":" + relativeFile))
	return "file:" + hex.EncodeToString(sum[:16])
}

// Rekey preserves observations and polling reservations when a credential identity changes.
func Rekey(ctx context.Context, path string, aliases map[string]string) error {
	db, err := open(path)
	if err != nil {
		return err
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Error("close quota history identities")
		}
	}()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if errRollback := tx.Rollback(); errRollback != nil && errRollback != sql.ErrTxDone {
			log.WithError(errRollback).Error("roll back quota history identities")
		}
	}()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS polls (credential TEXT PRIMARY KEY, next_at_ms INTEGER NOT NULL) WITHOUT ROWID`); err != nil {
		return err
	}
	for old, current := range aliases {
		if old == current {
			continue
		}
		var conflicts int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM observations a JOIN observations b
   ON b.credential=? AND a.window_id=b.window_id AND a.observed_at_ms=b.observed_at_ms
   WHERE a.credential=? AND (a.provider IS NOT b.provider OR a.label IS NOT b.label OR
   a.remaining IS NOT b.remaining OR a.reset_at_ms IS NOT b.reset_at_ms OR a.period_hours IS NOT b.period_hours)`, current, old).Scan(&conflicts)
		if err != nil {
			return err
		}
		if conflicts != 0 {
			return fmt.Errorf("quota history identity conflict: %d observations", conflicts)
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO observations
   SELECT ?, provider, window_id, label, observed_at_ms, remaining, reset_at_ms, period_hours
   FROM observations WHERE credential=?`, current, old); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM observations WHERE credential=?`, old); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO polls SELECT ?, next_at_ms FROM polls WHERE credential=?
   ON CONFLICT(credential) DO UPDATE SET next_at_ms=max(polls.next_at_ms,excluded.next_at_ms)`, current, old); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM polls WHERE credential=?`, old); err != nil {
			return err
		}
	}
	return tx.Commit()
}
