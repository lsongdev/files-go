// Package store keeps the file index: one row per file or directory, with
// the enrichment result stored as JSON on the same row.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

var ErrNotFound = errors.New("not found")

// schemaVersion is bumped on incompatible changes. The index is derived
// from the filesystem, so an old database is simply rebuilt.
const schemaVersion = 1

const schema = `
CREATE TABLE files (
	id       INTEGER PRIMARY KEY,
	root     TEXT    NOT NULL,
	parent   INTEGER,
	path     TEXT    NOT NULL,
	name     TEXT    NOT NULL,
	dir      INTEGER NOT NULL DEFAULT 0,
	size     INTEGER NOT NULL DEFAULT 0,
	mtime    INTEGER NOT NULL DEFAULT 0,
	kind     TEXT    NOT NULL DEFAULT '',
	title    TEXT    NOT NULL DEFAULT '',
	meta     TEXT,
	meta_ver INTEGER NOT NULL DEFAULT 0,
	meta_err TEXT,
	UNIQUE(root, path)
);
CREATE INDEX files_parent ON files(parent, dir DESC, name COLLATE NOCASE);
CREATE INDEX files_pending ON files(meta_ver);
CREATE INDEX files_kind ON files(kind);

CREATE VIRTUAL TABLE files_fts USING fts5(name, title, content='files', content_rowid='id', tokenize='trigram');
CREATE TRIGGER files_ai AFTER INSERT ON files BEGIN
	INSERT INTO files_fts(rowid, name, title) VALUES (new.id, new.name, new.title);
END;
CREATE TRIGGER files_ad AFTER DELETE ON files BEGIN
	INSERT INTO files_fts(files_fts, rowid, name, title) VALUES ('delete', old.id, old.name, old.title);
END;

CREATE TABLE cache (key TEXT PRIMARY KEY, value BLOB NOT NULL, at INTEGER NOT NULL);
`

// triggers are (re)created on every open so fixes apply without a rebuild.
const triggers = `
DROP TRIGGER IF EXISTS files_au;
CREATE TRIGGER files_au AFTER UPDATE OF name, title ON files
WHEN old.name IS NOT new.name OR old.title IS NOT new.title BEGIN
	INSERT INTO files_fts(files_fts, rowid, name, title) VALUES ('delete', old.id, old.name, old.title);
	INSERT INTO files_fts(rowid, name, title) VALUES (new.id, new.name, new.title);
END;
`

type File struct {
	ID      int64           `json:"id"`
	Root    string          `json:"root"`
	Parent  int64           `json:"parent,omitempty"`
	Path    string          `json:"-"`
	Name    string          `json:"name"`
	Dir     bool            `json:"dir"`
	Size    int64           `json:"size"` // bytes, or child count for directories
	MTime   int64           `json:"mtime"`
	Kind    string          `json:"kind,omitempty"`
	Title   string          `json:"-"`
	Meta    json.RawMessage `json:"meta,omitempty"`
	MetaVer int             `json:"-"`
	MetaErr string          `json:"error,omitempty"`
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version != schemaVersion {
		if err := reset(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("create schema: %w", err)
		}
	}
	if _, err := db.Exec(triggers); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func reset(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT type, name FROM sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'files_fts_%'`)
	if err != nil {
		return err
	}
	var drops []string
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			rows.Close()
			return err
		}
		drops = append(drops, fmt.Sprintf(`DROP %s IF EXISTS "%s"`, strings.ToUpper(kind), name))
	}
	rows.Close()
	for _, statement := range drops {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

const columns = `id, root, COALESCE(parent, 0), path, name, dir, size, mtime, kind, title, COALESCE(meta, ''), meta_ver, COALESCE(meta_err, '')`

func scanFile(row interface{ Scan(...any) error }) (File, error) {
	var f File
	var meta string
	err := row.Scan(&f.ID, &f.Root, &f.Parent, &f.Path, &f.Name, &f.Dir, &f.Size, &f.MTime, &f.Kind, &f.Title, &meta, &f.MetaVer, &f.MetaErr)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	if meta != "" {
		f.Meta = json.RawMessage(meta)
	}
	return f, err
}

func (s *Store) query(ctx context.Context, query string, args ...any) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []File{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (File, error) {
	return scanFile(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM files WHERE id=?`, id))
}

func (s *Store) ByPath(ctx context.Context, root, path string) (File, error) {
	return scanFile(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM files WHERE root=? AND path=?`, root, path))
}

// Children lists a directory: folders first, then files, by name.
func (s *Store) Children(ctx context.Context, parent int64, offset, limit int) ([]File, error) {
	return s.query(ctx, `SELECT `+columns+` FROM files WHERE parent=?
		ORDER BY dir DESC, name COLLATE NOCASE, id LIMIT ? OFFSET ?`, parent, limit, offset)
}

// ChildByName finds a direct child case-insensitively, e.g. poster.jpg.
func (s *Store) ChildByName(ctx context.Context, parent int64, names ...string) (File, error) {
	for _, name := range names {
		f, err := scanFile(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM files WHERE parent=? AND name=? COLLATE NOCASE LIMIT 1`, parent, name))
		if !errors.Is(err, ErrNotFound) {
			return f, err
		}
	}
	return File{}, ErrNotFound
}

// Ancestors returns the chain from the root directory down to (excluding) id.
func (s *Store) Ancestors(ctx context.Context, id int64) ([]File, error) {
	files, err := s.query(ctx, `WITH RECURSIVE up(id, depth) AS (
			SELECT parent, 1 FROM files WHERE id=? AND parent IS NOT NULL
			UNION ALL SELECT f.parent, up.depth+1 FROM files f JOIN up ON f.id=up.id WHERE f.parent IS NOT NULL
		) SELECT `+columns+` FROM files JOIN up USING(id) ORDER BY up.depth DESC`, id)
	return files, err
}

type SearchOptions struct {
	Query string
	Root  string
	Kind  string
	Limit int
}

// Search matches names and titles. The trigram index needs three characters;
// shorter queries (common for CJK) fall back to a LIKE scan.
func (s *Store) Search(ctx context.Context, opts SearchOptions) ([]File, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return []File{}, nil
	}
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = 200
	}
	where, args := []string{}, []any{}
	if opts.Root != "" {
		where, args = append(where, "root=?"), append(args, opts.Root)
	}
	if opts.Kind != "" {
		where, args = append(where, "kind=?"), append(args, opts.Kind)
	}
	if len([]rune(query)) >= 3 {
		where = append(where, `id IN (SELECT rowid FROM files_fts WHERE files_fts MATCH ?)`)
		args = append(args, `"`+strings.ReplaceAll(query, `"`, `""`)+`"`)
	} else {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query) + "%"
		where = append(where, `(name LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\')`)
		args = append(args, like, like)
	}
	args = append(args, opts.Limit)
	return s.query(ctx, `SELECT `+columns+` FROM files WHERE `+strings.Join(where, " AND ")+`
		ORDER BY dir DESC, length(name), name COLLATE NOCASE LIMIT ?`, args...)
}

// Cursor is a position in the pending queue, ordered by (meta_ver, id).
type Cursor struct {
	MetaVer int
	ID      int64
}

// Pending returns rows whose enrichment is older than version, after
// cursor. It is split in two queries so both seek in files_pending.
func (s *Store) Pending(ctx context.Context, version int, after Cursor, limit int) ([]File, error) {
	files, err := s.query(ctx, `SELECT `+columns+` FROM files INDEXED BY files_pending
		WHERE meta_ver=? AND id>? ORDER BY id LIMIT ?`, after.MetaVer, after.ID, limit)
	if err != nil || len(files) == limit {
		return files, err
	}
	more, err := s.query(ctx, `SELECT `+columns+` FROM files INDEXED BY files_pending
		WHERE meta_ver>? AND meta_ver<? ORDER BY meta_ver, id LIMIT ?`, after.MetaVer, version, limit-len(files))
	return append(files, more...), err
}

type Result struct {
	ID      int64
	Kind    string
	Title   string
	Meta    []byte // nil keeps no metadata
	Version int
	Err     string
}

// SaveResults stores enrichment results in one transaction.
func (s *Store) SaveResults(ctx context.Context, results []Result) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, `UPDATE files SET kind=?, title=?, meta=?, meta_ver=?, meta_err=NULLIF(?, '') WHERE id=?`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, r := range results {
		var meta any
		if r.Meta != nil {
			meta = string(r.Meta)
		}
		if _, err := statement.ExecContext(ctx, r.Kind, r.Title, meta, r.Version, r.Err, r.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Invalidate marks a row for re-enrichment.
func (s *Store) Invalidate(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE files SET meta_ver=0 WHERE id=?`, id)
	return err
}

type Stats struct {
	Files   int64 `json:"files"`
	Dirs    int64 `json:"dirs"`
	Pending int64 `json:"pending"`
	Failed  int64 `json:"failed"`
}

func (s *Store) Stats(ctx context.Context, version int) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM files WHERE dir=0),
		(SELECT COUNT(*) FROM files WHERE dir=1),
		(SELECT COUNT(*) FROM files WHERE meta_ver<?),
		(SELECT COUNT(*) FROM files WHERE meta_err IS NOT NULL)`, version).Scan(&st.Files, &st.Dirs, &st.Pending, &st.Failed)
	return st, err
}

// CacheGet returns a cached value younger than maxAge.
func (s *Store) CacheGet(ctx context.Context, key string, maxAge time.Duration) ([]byte, bool) {
	var value []byte
	var at int64
	if err := s.db.QueryRowContext(ctx, `SELECT value, at FROM cache WHERE key=?`, key).Scan(&value, &at); err != nil {
		return nil, false
	}
	return value, time.Since(time.Unix(at, 0)) < maxAge
}

// CachePrune deletes cache entries under prefix that are older than maxAge.
func (s *Store) CachePrune(ctx context.Context, prefix string, maxAge time.Duration) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cache WHERE key >= ? AND key < ? AND at < ?`,
		prefix, prefix+"\xff", time.Now().Add(-maxAge).Unix())
	return err
}

func (s *Store) CachePut(ctx context.Context, key string, value []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cache(key, value, at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, at=excluded.at`, key, value, time.Now().Unix())
	return err
}

// Version returns the enrichment version for a configuration tag. Versions
// only grow: a tag different from the last one gets a number above every
// stored row, so "meta_ver < version" means "needs enrichment".
func (s *Store) Version(ctx context.Context, tag string) (int, error) {
	var last struct {
		Tag     string
		Version int
	}
	if data, ok := s.CacheGet(ctx, "pipeline:version", 1<<62); ok && json.Unmarshal(data, &last) == nil && last.Tag == tag {
		return last.Version, nil
	}
	var highest int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(meta_ver), 0) FROM files`).Scan(&highest); err != nil {
		return 0, err
	}
	last.Tag, last.Version = tag, max(highest, last.Version)+1
	data, _ := json.Marshal(last)
	return last.Version, s.CachePut(ctx, "pipeline:version", data)
}
