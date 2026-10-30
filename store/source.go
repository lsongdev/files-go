package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/pkg/source"
)

var ErrMountChanged = errors.New("root mount identity changed")

func (s *Scanner) sourceRoot(ctx context.Context, root config.Root, force bool) (*source.Root, error) {
	r, err := source.OpenRoot(root.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOffline, err)
	}
	fail := func(e error) (*source.Root, error) { r.Close(); return nil, e }
	info, err := r.Stat()
	if err != nil {
		return fail(err)
	}
	device := fmt.Sprint(deviceOf(info))
	identity := device
	if root.Marker != "" {
		f, err := r.Open(root.Marker)
		if err != nil {
			return fail(fmt.Errorf("%w: marker: %v", ErrMountChanged, err))
		}
		b, err := source.ReadAll(f, 4096)
		f.Close()
		if err != nil {
			return fail(err)
		}
		identity += "\x00" + root.Marker + "\x00" + string(b)
	}
	var previous string
	err = s.store.db.QueryRowContext(ctx, `SELECT CAST(value AS TEXT) FROM cache WHERE key=?`, "mount:"+root.ID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	if errors.Is(err, sql.ErrNoRows) && !force {
		var legacy string
		e := s.store.db.QueryRowContext(ctx, `SELECT CAST(value AS TEXT) FROM cache WHERE key=?`, "root-device:"+root.ID).Scan(&legacy)
		if e == nil && legacy != device {
			return fail(ErrMountChanged)
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return fail(e)
		}
	}
	if err == nil && previous != identity && !force {
		return fail(ErrMountChanged)
	}
	// First access establishes identity; later changes require a forced scan.
	if (force && previous != identity) || errors.Is(err, sql.ErrNoRows) {
		if err := s.store.CachePut(ctx, "mount:"+root.ID, []byte(identity)); err != nil {
			return fail(err)
		}
	}
	return r, nil
}

// Open provides a regular, rooted read-only handle. Consumers, including
// subprocesses, must use this handle rather than resolve and reopen a path.
func (s *Scanner) Open(ctx context.Context, f File) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, ok := s.Root(f.Root)
	if !ok {
		return nil, fmt.Errorf("root %q is not configured", f.Root)
	}
	r, err := s.sourceRoot(ctx, root, false)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Open(f.Path)
}
