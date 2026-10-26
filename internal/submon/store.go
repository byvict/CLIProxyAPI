package submon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type store struct {
	db   *sql.DB
	lock *os.File
}
type storedAccount struct {
	ID           string
	Provider     provider
	Basis        identityBasis
	Label, Email string
}
type binding struct {
	SourceID, AccountID string
	From                time.Time
	To                  *time.Time
}
type storedAttempt struct {
	ID, SourceID, AccountID string
	At                      time.Time
	Status                  status
	Code                    string
	SideEffects             []string
}
type storedSample struct {
	AccountID, AttemptID string
	sample
}
type sourceState struct {
	NextAt   time.Time
	Failures int
}
type storedData struct {
	Accounts []storedAccount
	Bindings []binding
	Attempts []storedAttempt
	Samples  []storedSample
	Sources  map[string]sourceState
}

func openStore(dir string) (*store, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("data directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create monitor directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil || canonical != filepath.Clean(dir) {
		return nil, errors.New("monitor directory must be canonical without symlinks")
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return nil, fmt.Errorf("protect monitor directory: %w", err)
	}
	for _, name := range []string{"owner.lock", "history.sqlite", "history.sqlite-wal", "history.sqlite-shm", "access.key"} {
		info, errInfo := os.Lstat(filepath.Join(dir, name))
		if errInfo == nil && !info.Mode().IsRegular() {
			return nil, errors.New("monitor files must be regular files without symlinks")
		}
		if errInfo != nil && !errors.Is(errInfo, os.ErrNotExist) {
			return nil, errInfo
		}
	}
	lock, err := acquireLock(filepath.Join(dir, "owner.lock"))
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "history.sqlite")}
	q := u.Query()
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("open monitor database: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db, lock: lock}
	if err = s.migrate(); err != nil {
		_ = s.close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(dir, "history.sqlite"), 0600); err != nil {
		_ = s.close()
		return nil, err
	}
	return s, nil
}
func (s *store) close() error { return errors.Join(s.db.Close(), s.lock.Close()) }
func (s *store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return errors.New("monitor database schema is newer than this binary")
	}
	if version == 1 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
 CREATE TABLE accounts(id TEXT PRIMARY KEY,provider TEXT NOT NULL,basis TEXT NOT NULL,label TEXT NOT NULL,email TEXT NOT NULL);
 CREATE TABLE source_bindings(source_id TEXT NOT NULL,account_id TEXT NOT NULL REFERENCES accounts(id),from_ns INTEGER NOT NULL,to_ns INTEGER,PRIMARY KEY(source_id,from_ns));
 CREATE UNIQUE INDEX active_source ON source_bindings(source_id) WHERE to_ns IS NULL;
 CREATE TABLE attempts(id TEXT PRIMARY KEY,source_id TEXT NOT NULL,account_id TEXT,at_ns INTEGER NOT NULL,status TEXT NOT NULL,code TEXT NOT NULL,side_effects TEXT NOT NULL);
 CREATE INDEX attempt_source_time ON attempts(source_id,at_ns);
 CREATE TABLE readings(attempt_id TEXT NOT NULL REFERENCES attempts(id),account_id TEXT NOT NULL REFERENCES accounts(id),window_id TEXT NOT NULL,at_ns INTEGER NOT NULL,reading TEXT NOT NULL,PRIMARY KEY(attempt_id,window_id));
 CREATE INDEX reading_account_window_time ON readings(account_id,window_id,at_ns);
 CREATE TABLE source_states(source_id TEXT PRIMARY KEY,next_ns INTEGER NOT NULL,failures INTEGER NOT NULL);
 PRAGMA user_version=1;`)
	if err != nil {
		return fmt.Errorf("migrate monitor database: %w", err)
	}
	return tx.Commit()
}
func attemptID(p probe) string {
	if p.ID != "" {
		return p.ID
	}
	return p.SourceID + "/" + p.StartedAt.UTC().Format(time.RFC3339Nano)
}
func (s *store) reserve(id string, next time.Time, failures int) error {
	_, err := s.db.Exec(`INSERT INTO source_states VALUES(?,?,?) ON CONFLICT(source_id) DO UPDATE SET next_ns=excluded.next_ns,failures=excluded.failures`, id, next.UnixNano(), failures)
	return err
}
func (s *store) append(p probe, next time.Time, failures int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := attemptID(p)
	var exists int
	if err = tx.QueryRow("SELECT count(*) FROM attempts WHERE id=?", id).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	accountID := ""
	st, code := ok, ""
	effects, _ := json.Marshal(p.SideEffects)
	switch result := p.Outcome.(type) {
	case reading:
		accountID = result.Identity.accountID()
		i := result.Identity
		if _, err = tx.Exec(`INSERT INTO accounts VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET email=excluded.email`, accountID, i.Provider, i.Basis, string(i.Provider)+" · "+accountID[:6], i.Email); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE source_bindings SET to_ns=? WHERE source_id=? AND to_ns IS NULL AND account_id<>?`, p.At.UnixNano(), p.SourceID, accountID); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO source_bindings(source_id,account_id,from_ns) SELECT ?,?,? WHERE NOT EXISTS(SELECT 1 FROM source_bindings WHERE source_id=? AND to_ns IS NULL)`, p.SourceID, accountID, p.At.UnixNano(), p.SourceID); err != nil {
			return err
		}
	case missing:
		st, code = result.Status, result.Code
		if st == identityUnverified {
			if _, err = tx.Exec("UPDATE source_bindings SET to_ns=? WHERE source_id=? AND to_ns IS NULL", p.At.UnixNano(), p.SourceID); err != nil {
				return err
			}
		}
	default:
		return errors.New("missing probe outcome")
	}
	if _, err = tx.Exec("INSERT INTO attempts VALUES(?,?,?,?,?,?,?)", id, p.SourceID, accountID, p.At.UnixNano(), st, code, string(effects)); err != nil {
		return err
	}
	if result, valid := p.Outcome.(reading); valid {
		for _, w := range result.Windows {
			b, _ := json.Marshal(w)
			if _, err = tx.Exec("INSERT INTO readings VALUES(?,?,?,?,?)", id, accountID, w.Key.id(), p.At.UnixNano(), string(b)); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO source_states VALUES(?,?,?) ON CONFLICT(source_id) DO UPDATE SET next_ns=excluded.next_ns,failures=excluded.failures`, p.SourceID, next.UnixNano(), failures); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *store) load() (storedData, error) {
	data := storedData{Accounts: []storedAccount{}, Bindings: []binding{}, Attempts: []storedAttempt{}, Samples: []storedSample{}, Sources: map[string]sourceState{}}
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return data, err
	}
	defer tx.Rollback()
	queries := []struct {
		sql  string
		read func(*sql.Rows) error
	}{
		{"SELECT id,provider,basis,label,email FROM accounts ORDER BY provider,id", func(r *sql.Rows) error {
			var a storedAccount
			if err := r.Scan(&a.ID, &a.Provider, &a.Basis, &a.Label, &a.Email); err != nil {
				return err
			}
			data.Accounts = append(data.Accounts, a)
			return nil
		}},
		{"SELECT source_id,account_id,from_ns,to_ns FROM source_bindings ORDER BY from_ns", func(r *sql.Rows) error {
			var b binding
			var from int64
			var to sql.NullInt64
			if err := r.Scan(&b.SourceID, &b.AccountID, &from, &to); err != nil {
				return err
			}
			b.From = time.Unix(0, from)
			if to.Valid {
				v := time.Unix(0, to.Int64)
				b.To = &v
			}
			data.Bindings = append(data.Bindings, b)
			return nil
		}},
		{"SELECT id,source_id,account_id,at_ns,status,code,side_effects FROM attempts ORDER BY at_ns,id", func(r *sql.Rows) error {
			var a storedAttempt
			var ns int64
			var effects string
			if err := r.Scan(&a.ID, &a.SourceID, &a.AccountID, &ns, &a.Status, &a.Code, &effects); err != nil {
				return err
			}
			a.At = time.Unix(0, ns)
			if err := json.Unmarshal([]byte(effects), &a.SideEffects); err != nil {
				return err
			}
			data.Attempts = append(data.Attempts, a)
			return nil
		}},
		{"SELECT account_id,attempt_id,at_ns,reading FROM readings ORDER BY at_ns,attempt_id,window_id", func(r *sql.Rows) error {
			var s storedSample
			var ns int64
			var text string
			if err := r.Scan(&s.AccountID, &s.AttemptID, &ns, &text); err != nil {
				return err
			}
			s.At = time.Unix(0, ns)
			if err := json.Unmarshal([]byte(text), &s.Window); err != nil {
				return err
			}
			data.Samples = append(data.Samples, s)
			return nil
		}},
		{"SELECT source_id,next_ns,failures FROM source_states", func(r *sql.Rows) error {
			var id string
			var ns int64
			var s sourceState
			if err := r.Scan(&id, &ns, &s.Failures); err != nil {
				return err
			}
			s.NextAt = time.Unix(0, ns)
			data.Sources[id] = s
			return nil
		}},
	}
	for _, q := range queries {
		rows, err := tx.Query(q.sql)
		if err != nil {
			return data, err
		}
		for rows.Next() {
			if err = q.read(rows); err != nil {
				_ = rows.Close()
				return data, err
			}
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return data, err
		}
	}
	return data, tx.Commit()
}
