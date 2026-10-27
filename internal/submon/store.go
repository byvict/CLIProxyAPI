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

	log "github.com/sirupsen/logrus"
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
	NextAt    time.Time
	NotBefore time.Time
	Failures  int
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
	if version > 2 {
		return errors.New("monitor database schema is newer than this binary")
	}
	if version == 2 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollback(tx)
	if version == 0 {
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
	}
	_, err = tx.Exec(`
 ALTER TABLE source_states ADD COLUMN not_before_ns INTEGER NOT NULL DEFAULT 0;
 UPDATE source_states SET not_before_ns=next_ns;
 CREATE TABLE window_heads(account_id TEXT NOT NULL,window_id TEXT NOT NULL,attempt_id TEXT NOT NULL REFERENCES attempts(id),PRIMARY KEY(account_id,window_id));
 INSERT INTO window_heads SELECT account_id,window_id,attempt_id FROM readings r WHERE rowid=(SELECT rowid FROM readings latest WHERE latest.account_id=r.account_id AND latest.window_id=r.window_id ORDER BY at_ns DESC,attempt_id DESC LIMIT 1);
 CREATE INDEX reading_time ON readings(at_ns);
 CREATE INDEX attempt_time ON attempts(at_ns);
 PRAGMA user_version=2;`)
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
func (s *store) reserve(id string, next, notBefore time.Time, failures int) error {
	_, err := s.db.Exec(`INSERT INTO source_states VALUES(?,?,?,?) ON CONFLICT(source_id) DO UPDATE SET next_ns=excluded.next_ns,failures=excluded.failures,not_before_ns=excluded.not_before_ns`, id, next.UnixNano(), failures, timeNS(notBefore))
	return err
}
func (s *store) append(p probe, next time.Time, failures int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer rollback(tx)
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
	var i identity
	switch result := p.Outcome.(type) {
	case reading:
		i = result.Identity
	case missing:
		i = result.Identity
		st, code = result.Status, result.Code
		if st == identityUnverified || code == "credential_missing" || code == "api_key" || code == "unsupported_source" {
			if _, err = tx.Exec("UPDATE source_bindings SET to_ns=? WHERE source_id=? AND to_ns IS NULL", p.At.UnixNano(), p.SourceID); err != nil {
				return err
			}
		}
	default:
		return errors.New("missing probe outcome")
	}
	if i.Subject != "" {
		accountID = i.accountID()
		if _, err = tx.Exec(`INSERT INTO accounts VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET email=excluded.email`, accountID, i.Provider, i.Basis, string(i.Provider)+" · "+accountID[:6], i.Email); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE source_bindings SET to_ns=? WHERE source_id=? AND to_ns IS NULL AND account_id<>?`, p.At.UnixNano(), p.SourceID, accountID); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO source_bindings(source_id,account_id,from_ns) SELECT ?,?,? WHERE NOT EXISTS(SELECT 1 FROM source_bindings WHERE source_id=? AND to_ns IS NULL)`, p.SourceID, accountID, p.At.UnixNano(), p.SourceID); err != nil {
			return err
		}
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
			if _, err = tx.Exec(`INSERT INTO window_heads VALUES(?,?,?) ON CONFLICT(account_id,window_id) DO UPDATE SET attempt_id=excluded.attempt_id`, accountID, w.Key.id(), id); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO source_states VALUES(?,?,?,?) ON CONFLICT(source_id) DO UPDATE SET next_ns=excluded.next_ns,failures=excluded.failures,not_before_ns=excluded.not_before_ns`, p.SourceID, next.UnixNano(), failures, timeNS(p.NotBefore)); err != nil {
		return err
	}
	return tx.Commit()
}
func timeNS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func (s *store) loadStates() (map[string]sourceState, error) {
	rows, err := s.db.Query("SELECT source_id,next_ns,failures,not_before_ns FROM source_states")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	states := map[string]sourceState{}
	for rows.Next() {
		var id string
		var next, notBefore int64
		var state sourceState
		if err = rows.Scan(&id, &next, &state.Failures, &notBefore); err != nil {
			return nil, err
		}
		state.NextAt = time.Unix(0, next)
		if notBefore != 0 {
			state.NotBefore = time.Unix(0, notBefore)
		}
		states[id] = state
	}
	return states, rows.Err()
}

func (s *store) emailFor(id string) (string, error) {
	var email string
	err := s.db.QueryRow("SELECT email FROM accounts WHERE id=?", id).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return email, err
}

func (s *store) currentSources(id string) ([]string, error) {
	rows, err := s.db.Query("SELECT source_id FROM source_bindings WHERE account_id=? AND to_ns IS NULL", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var source string
		if err = rows.Scan(&source); err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	return out, rows.Err()
}

func (s *store) load() (storedData, error) { return s.loadSince(time.Time{}) }

func (s *store) loadSince(from time.Time) (storedData, error) {
	data := storedData{Accounts: []storedAccount{}, Bindings: []binding{}, Attempts: []storedAttempt{}, Samples: []storedSample{}, Sources: map[string]sourceState{}}
	readingsSQL := "SELECT account_id,attempt_id,at_ns,reading FROM readings"
	attemptsSQL := "SELECT id,source_id,account_id,at_ns,status,code,side_effects FROM attempts"
	if !from.IsZero() {
		cutoff := from.UnixNano()
		readingsSQL += fmt.Sprintf(" WHERE at_ns>=%d UNION SELECT r.account_id,r.attempt_id,r.at_ns,r.reading FROM readings r JOIN window_heads h ON h.account_id=r.account_id AND h.window_id=r.window_id AND h.attempt_id=r.attempt_id", cutoff)
		attemptsSQL += fmt.Sprintf(" WHERE at_ns>=%d OR id IN (SELECT attempt_id FROM window_heads) OR id IN (SELECT (SELECT a.id FROM attempts a WHERE a.source_id=s.source_id ORDER BY a.at_ns DESC,a.id DESC LIMIT 1) FROM source_states s)", cutoff)
	}
	readingsSQL += " ORDER BY at_ns,attempt_id"
	attemptsSQL += " ORDER BY at_ns,id"
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return data, err
	}
	defer rollback(tx)
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
		{attemptsSQL, func(r *sql.Rows) error {
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
		{readingsSQL, func(r *sql.Rows) error {
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
		{"SELECT source_id,next_ns,failures,not_before_ns FROM source_states", func(r *sql.Rows) error {
			var id string
			var ns int64
			var notBefore int64
			var s sourceState
			if err := r.Scan(&id, &ns, &s.Failures, &notBefore); err != nil {
				return err
			}
			s.NextAt = time.Unix(0, ns)
			if notBefore != 0 {
				s.NotBefore = time.Unix(0, notBefore)
			}
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

func rollback(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		log.WithField("component", "submon").Error("Failed to roll back quota persistence transaction")
	}
}
