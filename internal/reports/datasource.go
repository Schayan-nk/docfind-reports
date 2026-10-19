package reports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	// The SQL Server driver, for its side effect of registering "sqlserver"
	// with database/sql.
	//
	// It is already imported by internal/oa and internal/orgchart, so the
	// binary has had it all along -- but this package calling
	// sql.Open("sqlserver", ...) while depending on a DIFFERENT package to
	// have registered the name is the kind of coupling that works until
	// somebody trims an unused import. And it is not only theoretical: the
	// tests in this package would not have the driver at all.
	//
	// The MySQL driver arrives through driver.go, which imports it by name to
	// build a DSN through its config type.
	_ "github.com/microsoft/go-mssqldb"
)

// The databases an administrator has pointed the report builder at.
//
// docfind already talks to two servers it was configured for at start-up: the
// reporting MySQL and the automation system's SQL Server. This is different --
// these are connections somebody adds through the interface, at any time,
// without a restart and without editing a file on the server. That is the
// point of the feature and it is also why this file is careful:
//
//   - The connection string is a credential. It is sealed on the way in (see
//     secret.go) and NEVER returned to the browser, not even to the
//     administrator who typed it. The interface shows a redacted summary and
//     offers to replace it, which is the same treatment a password field gets
//     everywhere else in this application.
//   - A source is opened lazily and pooled, because a report list with six
//     sources must not open six connections to answer a page that runs one
//     query.
//   - The pool is small on purpose. These are somebody else's production
//     databases and a dashboard is not entitled to a hundred connections to
//     one of them.

// Driver names the engine. Two, because two is what was asked for and each
// needs its own DSN shape; a third would need its own driver import as well.
type Driver string

const (
	DriverMySQL     Driver = "mysql"
	DriverSQLServer Driver = "sqlserver"
)

func (d Driver) valid() bool { return d == DriverMySQL || d == DriverSQLServer }

// Label is what the interface calls this engine.
func (d Driver) Label() string {
	switch d {
	case DriverMySQL:
		return "MySQL / MariaDB"
	case DriverSQLServer:
		return "SQL Server"
	}
	return string(d)
}

// DataSource is one database the builder can query.
type DataSource struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver Driver `json:"driver"`
	// DSN is sealed on disk and blank in anything sent to a browser. The
	// json tag is deliberately not "-": it has to round-trip through the
	// file. Handing it out is prevented by Public(), which every route uses.
	DSN string `json:"dsn"`
	// Note is the administrator's own reminder of what this server is for.
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PublicSource is the shape a browser is allowed to see.
//
// A separate type rather than a cleared field, because "remember to blank the
// DSN" is a rule somebody eventually forgets on a new route, and a type that
// has no such field cannot leak it.
type PublicSource struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Driver    Driver    `json:"driver"`
	DriverLbl string    `json:"driver_label"`
	Note      string    `json:"note,omitempty"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Public describes the source without its secret.
//
// Summary is built from the DSN rather than stored, and shows the parts that
// identify a server without the parts that authenticate to it: host, port and
// database. That is what somebody needs to recognise which connection a
// report is using; the password is not.
func (s DataSource) Public(plainDSN string) PublicSource {
	return PublicSource{
		ID: s.ID, Name: s.Name, Driver: s.Driver, DriverLbl: s.Driver.Label(),
		Note: s.Note, Summary: describeDSN(s.Driver, plainDSN),
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

// SourceStore keeps them in a JSON file and pools the connections.
type SourceStore struct {
	mu      sync.RWMutex
	path    string
	box     *secretBox
	sources map[string]DataSource
	order   []string
	// pools are opened on first use and kept. Keyed by source id, dropped
	// when a source is edited or removed so the next query picks up the new
	// address rather than the old one.
	pools map[string]*sql.DB
	// guard vets where a connection points (datasourceguard.go).
	guard guardSlot
}

// OpenSourceStore loads the file, creating the key beside it on first use.
func OpenSourceStore(path string) (*SourceStore, error) {
	box, err := openSecretBox(path)
	if err != nil {
		return nil, err
	}
	s := &SourceStore{
		path: path, box: box,
		sources: map[string]DataSource{},
		pools:   map[string]*sql.DB{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SourceStore) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // nothing configured yet is the normal first state
	}
	if err != nil {
		return fmt.Errorf("reports: reading %s: %w", s.path, err)
	}
	var list []DataSource
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("reports: %s is not valid JSON: %w", s.path, err)
	}
	for _, d := range list {
		s.sources[d.ID] = d
		s.order = append(s.order, d.ID)
	}
	sort.Strings(s.order)
	return nil
}

// save writes the whole file. Written to a temporary name and renamed, so an
// interrupted write cannot leave a half a file where every stored connection
// used to be.
func (s *SourceStore) save() error {
	list := make([]DataSource, 0, len(s.order))
	for _, id := range s.order {
		list = append(list, s.sources[id])
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("reports: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("reports: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("reports: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("reports: replacing %s: %w", s.path, err)
	}
	return nil
}

// List returns every source, without secrets.
func (s *SourceStore) List() []PublicSource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PublicSource, 0, len(s.order))
	for _, id := range s.order {
		d := s.sources[id]
		plain, err := s.box.Open(d.DSN)
		if err != nil {
			// A source whose key no longer opens it is shown, not hidden: the
			// administrator has to be able to see it in order to replace it.
			out = append(out, PublicSource{
				ID: d.ID, Name: d.Name, Driver: d.Driver, DriverLbl: d.Driver.Label(),
				Note: d.Note, Summary: "connection string unreadable -- re-enter it",
				CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
			})
			continue
		}
		out = append(out, d.Public(plain))
	}
	return out
}

// Count is how many are configured.
func (s *SourceStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sources)
}

// Has reports whether an id names a source.
func (s *SourceStore) Has(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.sources[id]
	return ok
}

// DriverOf reports which engine a source speaks.
//
// Separate from pool, which would answer the same question but only by opening
// a connection first. Filter validation needs the engine at save time, when the
// database may well be unreachable -- refusing to save a report because the
// server is down would be the wrong answer to the wrong question.
func (s *SourceStore) DriverOf(id string) (Driver, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.sources[id]
	if !ok {
		return "", false
	}
	return d.Driver, true
}

// Save adds or replaces a source.
//
// An empty dsn on an existing source means "leave the connection string
// alone", which is what lets somebody rename a source or fix its note without
// re-typing a password they may not have to hand.
func (s *SourceStore) Save(d DataSource, dsn string) (PublicSource, error) {
	d.ID = strings.TrimSpace(d.ID)
	d.Name = strings.TrimSpace(d.Name)
	dsn = strings.TrimSpace(dsn)

	if d.Name == "" {
		return PublicSource{}, errors.New("the connection needs a name")
	}
	if !d.Driver.valid() {
		return PublicSource{}, fmt.Errorf("%q is not an engine this can use; "+
			"choose %s or %s", d.Driver, DriverMySQL, DriverSQLServer)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, isEdit := s.sources[d.ID]
	if d.ID == "" {
		id, err := newSourceID()
		if err != nil {
			return PublicSource{}, err
		}
		d.ID = id
	}
	if !isEdit && dsn == "" {
		return PublicSource{}, errors.New("a new connection needs a connection string")
	}

	now := time.Now().UTC()
	d.UpdatedAt = now
	if isEdit {
		d.CreatedAt = existing.CreatedAt
		if dsn == "" {
			d.DSN = existing.DSN // keep the stored secret
		}
	} else {
		d.CreatedAt = now
	}

	plain := dsn
	if dsn != "" {
		sealed, err := s.box.Seal(dsn)
		if err != nil {
			return PublicSource{}, err
		}
		d.DSN = sealed
	} else {
		var err error
		if plain, err = s.box.Open(d.DSN); err != nil {
			return PublicSource{}, err
		}
	}

	s.sources[d.ID] = d
	if !isEdit {
		s.order = append(s.order, d.ID)
		sort.Strings(s.order)
	}
	if err := s.save(); err != nil {
		return PublicSource{}, err
	}
	// The address may have changed, so the pooled connection is no longer the
	// one this source describes.
	s.dropPoolLocked(d.ID)
	return d.Public(plain), nil
}

// Delete forgets a source and closes its pool.
func (s *SourceStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sources[id]; !ok {
		return fmt.Errorf("no such connection")
	}
	delete(s.sources, id)
	out := s.order[:0]
	for _, x := range s.order {
		if x != id {
			out = append(out, x)
		}
	}
	s.order = out
	s.dropPoolLocked(id)
	return s.save()
}

func (s *SourceStore) dropPoolLocked(id string) {
	if db, ok := s.pools[id]; ok {
		db.Close()
		delete(s.pools, id)
	}
}

// Close shuts every pool. The stored file is left alone.
func (s *SourceStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, db := range s.pools {
		db.Close()
		delete(s.pools, id)
	}
}

// pool returns the connection for a source, opening it on first use.
func (s *SourceStore) pool(id string) (*sql.DB, Driver, error) {
	s.mu.RLock()
	if db, ok := s.pools[id]; ok {
		d := s.sources[id]
		s.mu.RUnlock()
		return db, d.Driver, nil
	}
	d, known := s.sources[id]
	s.mu.RUnlock()

	// Checked before the pool exists, outside the lock: the check resolves a
	// name, and nothing else should wait on DNS.
	if known {
		dsn, err := s.box.Open(d.DSN)
		if err != nil {
			return nil, "", err
		}
		if err := s.checkGuard(context.Background(), d.Driver, dsn); err != nil {
			return nil, "", err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Checked again under the write lock: two requests can arrive together and
	// only one pool should exist.
	if db, ok := s.pools[id]; ok {
		return db, s.sources[id].Driver, nil
	}
	d, ok := s.sources[id]
	if !ok {
		return nil, "", fmt.Errorf("no such connection")
	}
	dsn, err := s.box.Open(d.DSN)
	if err != nil {
		return nil, "", err
	}
	db, err := sql.Open(string(d.Driver), dsn)
	if err != nil {
		return nil, "", fmt.Errorf("opening %s: %w", d.Name, err)
	}
	// Deliberately small. Somebody else's production database does not owe a
	// dashboard a wide pool, and a report that needs more than four
	// simultaneous connections is a report that should be one query.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(15 * time.Minute)
	s.pools[id] = db
	return db, d.Driver, nil
}

func sinceMs(t time.Time) int64 { return time.Since(t).Milliseconds() }

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// redactDSNError keeps a connection string out of an error shown in a browser.
//
// Both drivers put the DSN into their failure messages, which is reasonable
// for a log on a server and not reasonable for a page: "dial tcp: connection
// refused" is the useful part and the password beside it is not.
func redactDSNError(msg, dsn string) string {
	if dsn != "" {
		msg = strings.ReplaceAll(msg, dsn, "<connection string>")
	}
	// And the password on its own, in case the driver quoted only part of it.
	if pw := passwordOf(dsn); pw != "" && len(pw) > 2 {
		msg = strings.ReplaceAll(msg, pw, "<password>")
	}
	return msg
}
