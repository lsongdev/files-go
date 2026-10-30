package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Match identifies a catalog resource. Provider-specific validation and API
// calls belong to media, not the index.
type Match struct {
	Provider string `json:"provider"`
	Type     string `json:"type"`
	ID       string `json:"id"`
}

// Match returns the most specific persistent choice for a file. Directory
// choices apply to their subtree; a file-specific choice takes precedence.
func (s *Store) Match(ctx context.Context, f File) (*Match, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT choice FROM overrides WHERE root=?
		AND (path=? OR path='' OR (? > path||'/' AND ? < path||'0'))
		ORDER BY length(path) DESC LIMIT 1`, f.Root, f.Path, f.Path, f.Path).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Match
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// MatchOwner finds the indexed scope of an inherited manual choice.
func (s *Store) MatchOwner(ctx context.Context, f File) (File, error) {
	var path string
	err := s.db.QueryRowContext(ctx, `SELECT path FROM overrides WHERE root=?
		AND (path=? OR path='' OR (? > path||'/' AND ? < path||'0'))
		ORDER BY length(path) DESC LIMIT 1`, f.Root, f.Path, f.Path, f.Path).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, err
	}
	return s.ByPath(ctx, f.Root, path)
}

// SetMatch persists a choice separately from derived metadata and invalidates
// its scope atomically. Choices survive deletion/reappearance at the same path
// and removal of a root; renames/moves do not transfer choices automatically.
func (s *Store) SetMatch(ctx context.Context, scope File, m *Match) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if m == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM overrides WHERE root=? AND path=?`, scope.Root, scope.Path)
	} else {
		data, e := json.Marshal(m)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO overrides(root,path,choice) VALUES(?,?,?)
			ON CONFLICT(root,path) DO UPDATE SET choice=excluded.choice`, scope.Root, scope.Path, string(data))
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE files SET meta_ver=0,revision=revision+1 WHERE root=?
		AND (path=? OR ?='' OR (path>? AND path<?))`, scope.Root, scope.Path, scope.Path, scope.Path+"/", scope.Path+"0")
	if err != nil {
		return err
	}
	return tx.Commit()
}

// MediaUnder returns all indexed audio/video files in a subtree, in stable
// path order. It also handles the root directory (whose path is empty).
func (s *Store) MediaUnder(ctx context.Context, dir File) ([]File, error) {
	return s.query(ctx, `SELECT `+columns+` FROM files WHERE root=? AND dir=0 AND kind IN ('video','audio')
		AND (?='' OR (path>? AND path<?)) ORDER BY path`, dir.Root, dir.Path, dir.Path+"/", dir.Path+"0")
}
