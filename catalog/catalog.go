package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lsongdev/files-go/model"
)

var ErrNotFound = errors.New("catalog entry not found")

type Catalog struct {
	db     *sql.DB
	reader *sql.DB
}

func New(db *sql.DB) *Catalog { return &Catalog{db: db, reader: db} }

func NewWithReader(db, reader *sql.DB) *Catalog {
	if reader == nil {
		reader = db
	}
	return &Catalog{db: db, reader: reader}
}

type ListCursor struct {
	DirectoryRank int
	Name          string
	ID            string
}

type ListOptions struct {
	Limit int
	After *ListCursor
}

type SearchOptions struct {
	Query     string
	LibraryID string
	Type      model.EntryType
	Extension string
	Limit     int
}

type ScanSession struct {
	Generation  int64
	Entries     int64
	Files       int64
	Directories int64
	Resumed     bool
}

type ScanDirectoryCheckpoint struct {
	Listed   bool
	Complete bool
}

const entryColumns = `id, storage_id, parent_id, name, path, type, size, mtime,
	inode, device, mime, extension, available, scan_generation, created_at, updated_at`

const qualifiedEntryColumns = `e.id, e.storage_id, e.parent_id, e.name, e.path, e.type, e.size, e.mtime,
	e.inode, e.device, e.mime, e.extension, e.available, e.scan_generation, e.created_at, e.updated_at`

type scanner interface{ Scan(...any) error }

func scanEntry(row scanner) (model.Entry, error) {
	var entry model.Entry
	var parent sql.NullString
	var mtime sql.NullTime
	var inode, device sql.NullInt64
	var available int
	err := row.Scan(&entry.ID, &entry.StorageID, &parent, &entry.Name, &entry.Path, &entry.Type,
		&entry.Size, &mtime, &inode, &device, &entry.MIME, &entry.Extension, &available,
		&entry.ScanGeneration, &entry.CreatedAt, &entry.UpdatedAt)
	if parent.Valid {
		entry.ParentID = &parent.String
	}
	if mtime.Valid {
		entry.ModifiedAt = mtime.Time
	}
	if inode.Valid {
		entry.Inode = uint64(inode.Int64)
	}
	if device.Valid {
		entry.Device = uint64(device.Int64)
	}
	entry.Available = available != 0
	return entry, err
}

func (c *Catalog) Entry(ctx context.Context, id string) (*model.Entry, error) {
	entry, err := scanEntry(c.reader.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (c *Catalog) EntryByPath(ctx context.Context, storageID, path string) (*model.Entry, error) {
	entry, err := scanEntry(c.reader.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE storage_id = ? AND path = ?`, storageID, path))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (c *Catalog) Children(ctx context.Context, parentID string, opts ListOptions) ([]model.Entry, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	args := []any{parentID}
	where := "parent_id = ?"
	if opts.After != nil {
		where += ` AND ((CASE WHEN type = 'directory' THEN 0 ELSE 1 END) > ?
			OR ((CASE WHEN type = 'directory' THEN 0 ELSE 1 END) = ? AND lower(name) > ?)
			OR ((CASE WHEN type = 'directory' THEN 0 ELSE 1 END) = ? AND lower(name) = ? AND id > ?))`
		args = append(args, opts.After.DirectoryRank, opts.After.DirectoryRank, opts.After.Name,
			opts.After.DirectoryRank, opts.After.Name, opts.After.ID)
	}
	args = append(args, limit+1)
	rows, err := c.reader.QueryContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE `+where+`
		ORDER BY CASE WHEN type = 'directory' THEN 0 ELSE 1 END, lower(name), id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Entry, 0, limit+1)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, entry)
	}
	return items, rows.Err()
}

func (c *Catalog) Search(ctx context.Context, opts SearchOptions) ([]model.Entry, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := ftsPrefixQuery(opts.Query)
	if query == "" {
		return []model.Entry{}, nil
	}

	where := []string{"entry_search MATCH ?"}
	args := []any{query}
	if opts.LibraryID != "" {
		where = append(where, `EXISTS (
			SELECT 1 FROM library_sources ls
			WHERE ls.library_id = ? AND ls.storage_id = e.storage_id
			AND (ls.path = '' OR e.path = ls.path OR substr(e.path, 1, length(ls.path) + 1) = ls.path || '/')
		)`)
		args = append(args, opts.LibraryID)
	}
	if opts.Type != "" {
		where = append(where, "e.type = ?")
		args = append(args, opts.Type)
	}
	if opts.Extension != "" {
		where = append(where, "lower(e.extension) = ?")
		args = append(args, strings.ToLower(strings.TrimPrefix(opts.Extension, ".")))
	}
	args = append(args, limit)

	rows, err := c.reader.QueryContext(ctx, `SELECT `+qualifiedEntryColumns+`
		FROM entry_search JOIN entries e ON e.id = entry_search.entry_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY bm25(entry_search), CASE WHEN e.type = 'directory' THEN 0 ELSE 1 END, lower(e.name), e.id
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Entry, 0, limit)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, entry)
	}
	return items, rows.Err()
}

func (c *Catalog) AddEntry(ctx context.Context, entry model.Entry) (*model.Entry, error) {
	generation, err := c.mutationGeneration(ctx, c.db, entry.StorageID)
	if err != nil {
		return nil, err
	}
	entries, err := c.UpsertEntries(ctx, []model.Entry{entry}, generation)
	if err != nil {
		return nil, err
	}
	return &entries[0], nil
}

func (c *Catalog) MoveEntry(ctx context.Context, id, parentID, name, newPath string) (*model.Entry, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var storageID, oldPath string
	if err := tx.QueryRowContext(ctx, `SELECT storage_id, path FROM entries WHERE id=?`, id).Scan(&storageID, &oldPath); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	generation, err := c.mutationGeneration(ctx, tx, storageID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE entries
		SET path=? || substr(path, length(?) + 1), scan_generation=?, updated_at=?
		WHERE storage_id=? AND substr(path, 1, length(?) + 1)=? || '/'`,
		newPath, oldPath, generation, now, storageID, oldPath, oldPath); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE entries SET parent_id=?, name=?, path=?, scan_generation=?, updated_at=? WHERE id=?`, parentID, name, newPath, generation, now, id)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return c.Entry(ctx, id)
}

func (c *Catalog) DeleteEntry(ctx context.Context, id string) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var storageID, entryPath string
	if err := tx.QueryRowContext(ctx, `SELECT storage_id, path FROM entries WHERE id=?`, id).Scan(&storageID, &entryPath); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entries
		WHERE storage_id=? AND (path=? OR substr(path, 1, length(?) + 1)=? || '/')`, storageID, entryPath, entryPath, entryPath); err != nil {
		return err
	}
	return tx.Commit()
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (c *Catalog) mutationGeneration(ctx context.Context, queryer queryRower, storageID string) (int64, error) {
	var generation int64
	var state string
	if err := queryer.QueryRowContext(ctx, `SELECT scan_generation, state FROM storages WHERE id=?`, storageID).Scan(&generation, &state); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if state == "scanning" {
		generation++
	}
	return generation, nil
}

func ftsPrefixQuery(value string) string {
	parts := strings.Fields(strings.TrimSpace(value))
	for index, part := range parts {
		parts[index] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"*`
	}
	return strings.Join(parts, " ")
}

func (c *Catalog) RegisterStorage(ctx context.Context, id, name, storageType string) error {
	now := time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `INSERT INTO storages(id, name, type, state, created_at, updated_at)
		VALUES (?, ?, ?, 'unknown', ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, type=excluded.type, updated_at=excluded.updated_at`,
		id, name, storageType, now, now)
	return err
}

func (c *Catalog) RegisterLibrary(ctx context.Context, library model.Library) error {
	now := time.Now().UTC()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO libraries(id, name, type, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, type=excluded.type, updated_at=excluded.updated_at`, library.ID, library.Name, library.Type, now, now); err != nil {
		return err
	}
	for _, source := range library.Sources {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_sources(library_id, storage_id, path, created_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(library_id, storage_id, path) DO NOTHING`, library.ID, source.StorageID, source.Path, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Catalog) EnsureRoot(ctx context.Context, storageID string, generation int64) (*model.Entry, error) {
	entry := model.Entry{ID: uuid.Must(uuid.NewV7()).String(), StorageID: storageID, Name: "", Path: "", Type: model.EntryDirectory, Available: true, ScanGeneration: generation}
	entries, err := c.UpsertEntries(ctx, []model.Entry{entry}, generation)
	if err != nil {
		return nil, err
	}
	return &entries[0], nil
}

func (c *Catalog) BeginScan(ctx context.Context, storageID string) (int64, error) {
	session, err := c.BeginScanSession(ctx, storageID)
	return session.Generation, err
}

func (c *Catalog) BeginScanSession(ctx context.Context, storageID string) (ScanSession, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return ScanSession{}, err
	}
	defer tx.Rollback()
	var currentGeneration, entries, files, directories int64
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT scan_generation, state, scan_entries, scan_files, scan_directories
		FROM storages WHERE id=?`, storageID).Scan(&currentGeneration, &state, &entries, &files, &directories); errors.Is(err, sql.ErrNoRows) {
		return ScanSession{}, fmt.Errorf("storage %q is not registered", storageID)
	} else if err != nil {
		return ScanSession{}, err
	}
	generation := currentGeneration + 1
	var checkpointCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_checkpoints WHERE storage_id=? AND generation=?`, storageID, generation).Scan(&checkpointCount); err != nil {
		return ScanSession{}, err
	}
	resumed := state == "interrupted" && checkpointCount > 0
	now := time.Now().UTC()
	if resumed {
		if _, err := tx.ExecContext(ctx, `UPDATE storages SET state='scanning', scan_updated_at=?, scan_error=NULL, updated_at=? WHERE id=?`, now, now, storageID); err != nil {
			return ScanSession{}, err
		}
	} else {
		entries, files, directories = 0, 0, 0
		if _, err := tx.ExecContext(ctx, `DELETE FROM scan_checkpoints WHERE storage_id=?`, storageID); err != nil {
			return ScanSession{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE storages SET state='scanning', scan_started_at=?, scan_updated_at=?,
			scan_entries=0, scan_files=0, scan_directories=0,
			scan_estimate=(SELECT COUNT(*) FROM entries WHERE storage_id=?), scan_error=NULL, updated_at=? WHERE id=?`, now, now, storageID, now, storageID); err != nil {
			return ScanSession{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ScanSession{}, err
	}
	return ScanSession{Generation: generation, Entries: entries, Files: files, Directories: directories, Resumed: resumed}, nil
}

func (c *Catalog) ScanCheckpoint(ctx context.Context, storageID string, generation int64, path string) (ScanDirectoryCheckpoint, error) {
	var listed, complete int
	err := c.reader.QueryRowContext(ctx, `SELECT listed, complete FROM scan_checkpoints WHERE storage_id=? AND generation=? AND path=?`, storageID, generation, path).Scan(&listed, &complete)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanDirectoryCheckpoint{}, nil
	}
	return ScanDirectoryCheckpoint{Listed: listed != 0, Complete: complete != 0}, err
}

func (c *Catalog) MarkScanDirectoryListed(ctx context.Context, storageID string, generation int64, path string) error {
	now := time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `INSERT INTO scan_checkpoints(storage_id, generation, path, listed, complete, updated_at)
		VALUES (?, ?, ?, 1, 0, ?) ON CONFLICT(storage_id, generation, path) DO UPDATE SET listed=1, updated_at=excluded.updated_at`, storageID, generation, path, now)
	return err
}

func (c *Catalog) MarkScanDirectoryComplete(ctx context.Context, storageID string, generation int64, path string) error {
	now := time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `INSERT INTO scan_checkpoints(storage_id, generation, path, listed, complete, updated_at)
		VALUES (?, ?, ?, 1, 1, ?) ON CONFLICT(storage_id, generation, path) DO UPDATE SET listed=1, complete=1, updated_at=excluded.updated_at`, storageID, generation, path, now)
	return err
}

func (c *Catalog) UpsertEntries(ctx context.Context, entries []model.Entry, generation int64) ([]model.Entry, error) {
	if len(entries) == 0 {
		return entries, nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for index := range entries {
		entry := &entries[index]
		entry.ScanGeneration = generation
		entry.Available = true
		if entry.ID == "" {
			entry.ID = uuid.Must(uuid.NewV7()).String()
		}

		// A previous-generation local entry with the same device/inode is a rename.
		renamed := false
		if entry.Inode != 0 && entry.Device != 0 {
			var existingID string
			err := tx.QueryRowContext(ctx, `SELECT id FROM entries
				WHERE storage_id=? AND device=? AND inode=? AND path<>? AND scan_generation<?
				ORDER BY updated_at DESC LIMIT 1`, entry.StorageID, int64(entry.Device), int64(entry.Inode), entry.Path, generation).Scan(&existingID)
			if err == nil {
				entry.ID = existingID
				renamed = true
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}

		var parent any
		if entry.ParentID != nil {
			parent = *entry.ParentID
		}
		if renamed {
			result, err := tx.ExecContext(ctx, `UPDATE entries SET parent_id=?, name=?, path=?, type=?, size=?, mtime=?, inode=?, device=?, mime=?, extension=?, available=1, scan_generation=?, updated_at=? WHERE id=?`,
				parent, entry.Name, entry.Path, entry.Type, entry.Size, nullableTime(entry.ModifiedAt), nullableUint(entry.Inode), nullableUint(entry.Device), entry.MIME, entry.Extension, generation, now, entry.ID)
			if err != nil {
				return nil, err
			}
			if affected, _ := result.RowsAffected(); affected != 1 {
				return nil, fmt.Errorf("rename entry %s: expected one affected row", entry.ID)
			}
			if err := tx.QueryRowContext(ctx, `SELECT created_at, updated_at FROM entries WHERE id=?`, entry.ID).Scan(&entry.CreatedAt, &entry.UpdatedAt); err != nil {
				return nil, err
			}
			continue
		}
		row := tx.QueryRowContext(ctx, `INSERT INTO entries
			(id, storage_id, parent_id, name, path, type, size, mtime, inode, device, mime, extension, available, scan_generation, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
			ON CONFLICT(storage_id, path) DO UPDATE SET
				parent_id=excluded.parent_id, name=excluded.name, type=excluded.type, size=excluded.size,
				mtime=excluded.mtime, inode=excluded.inode, device=excluded.device, mime=excluded.mime,
				extension=excluded.extension, available=1, scan_generation=excluded.scan_generation, updated_at=excluded.updated_at
			RETURNING id, created_at, updated_at`, entry.ID, entry.StorageID, parent, entry.Name, entry.Path,
			entry.Type, entry.Size, nullableTime(entry.ModifiedAt), nullableUint(entry.Inode), nullableUint(entry.Device),
			entry.MIME, entry.Extension, generation, now, now)
		var actualID string
		if err := row.Scan(&actualID, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		entry.ID = actualID
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return entries, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableUint(value uint64) any {
	if value == 0 {
		return nil
	}
	// SQLite INTEGER is signed. Preserve the uint64 bit pattern (macOS
	// device numbers can have the high bit set) instead of passing uint64
	// to database/sql, which rejects values above MaxInt64.
	return int64(value)
}

func (c *Catalog) CompleteScan(ctx context.Context, storageID string, generation int64) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE entries SET available=0, updated_at=? WHERE storage_id=? AND scan_generation<?`, now, storageID, generation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE storages SET state='online', last_seen_at=?, last_scan_at=?, scan_updated_at=?, scan_generation=?,
		scan_entries=(SELECT COUNT(*) FROM entries WHERE storage_id=? AND scan_generation=?),
		scan_files=(SELECT COUNT(*) FROM entries WHERE storage_id=? AND scan_generation=? AND type='file'),
		scan_directories=(SELECT COUNT(*) FROM entries WHERE storage_id=? AND scan_generation=? AND type='directory'),
		scan_error=NULL, updated_at=? WHERE id=?`, now, now, now, generation, storageID, generation, storageID, generation, storageID, generation, now, storageID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_checkpoints WHERE storage_id=? AND generation<=?`, storageID, generation); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Catalog) FailScan(ctx context.Context, storageID, state, message string) error {
	if state != "offline" && state != "interrupted" {
		state = "error"
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE storages SET state=?, scan_updated_at=?, scan_error=?, updated_at=? WHERE id=?`, state, now, message, now, storageID); err != nil {
		return err
	}
	if state == "offline" {
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET available=0, updated_at=? WHERE storage_id=?`, now, storageID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Catalog) Storages(ctx context.Context) ([]model.Storage, error) {
	rows, err := c.reader.QueryContext(ctx, `SELECT id, name, type, state, last_seen_at, last_scan_at, scan_started_at, scan_updated_at,
		scan_generation, scan_entries, scan_files, scan_directories, scan_estimate, COALESCE(scan_error, '') FROM storages ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.Storage
	for rows.Next() {
		var item model.Storage
		var seen, scan, started, updated sql.NullTime
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.State, &seen, &scan, &started, &updated,
			&item.ScanGeneration, &item.ScanEntries, &item.ScanFiles, &item.ScanDirectories, &item.ScanEstimate, &item.ScanError); err != nil {
			return nil, err
		}
		if seen.Valid {
			item.LastSeenAt = &seen.Time
		}
		if scan.Valid {
			item.LastScanAt = &scan.Time
		}
		if started.Valid {
			item.ScanStartedAt = &started.Time
		}
		if updated.Valid {
			item.ScanUpdatedAt = &updated.Time
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (c *Catalog) Storage(ctx context.Context, id string) (*model.Storage, error) {
	var item model.Storage
	var seen, scan, started, updated sql.NullTime
	err := c.reader.QueryRowContext(ctx, `SELECT id, name, type, state, last_seen_at, last_scan_at, scan_started_at, scan_updated_at,
		scan_generation, scan_entries, scan_files, scan_directories, scan_estimate, COALESCE(scan_error, '') FROM storages WHERE id=?`, id).
		Scan(&item.ID, &item.Name, &item.Type, &item.State, &seen, &scan, &started, &updated,
			&item.ScanGeneration, &item.ScanEntries, &item.ScanFiles, &item.ScanDirectories, &item.ScanEstimate, &item.ScanError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if seen.Valid {
		item.LastSeenAt = &seen.Time
	}
	if scan.Valid {
		item.LastScanAt = &scan.Time
	}
	if started.Valid {
		item.ScanStartedAt = &started.Time
	}
	if updated.Valid {
		item.ScanUpdatedAt = &updated.Time
	}
	return &item, nil
}

func (c *Catalog) UpdateScanProgress(ctx context.Context, storageID string, entries, files, directories int64) error {
	now := time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `UPDATE storages SET scan_entries=?, scan_files=?, scan_directories=?, scan_updated_at=?, updated_at=?
		WHERE id=? AND state='scanning'`, entries, files, directories, now, now, storageID)
	return err
}

func (c *Catalog) RecoverInterruptedScans(ctx context.Context) error {
	now := time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `UPDATE storages SET state='interrupted', scan_updated_at=?,
		scan_error='service stopped before scan completed', updated_at=?
		WHERE state='scanning' OR (state='error' AND scan_error='context canceled')`, now, now)
	return err
}

func (c *Catalog) NeedsInitialScan(ctx context.Context, storageID string) (bool, error) {
	var count int64
	err := c.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE storage_id=?`, storageID).Scan(&count)
	return count == 0, err
}

func (c *Catalog) Libraries(ctx context.Context) ([]model.Library, error) {
	rows, err := c.reader.QueryContext(ctx, `SELECT id, name, type FROM libraries ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.Library
	for rows.Next() {
		var item model.Library
		if err := rows.Scan(&item.ID, &item.Name, &item.Type); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range items {
		sources, err := c.librarySources(ctx, items[index].ID)
		if err != nil {
			return nil, err
		}
		items[index].Sources = sources
	}
	return items, nil
}

func (c *Catalog) librarySources(ctx context.Context, libraryID string) ([]model.LibrarySource, error) {
	rows, err := c.reader.QueryContext(ctx, `SELECT ls.storage_id, ls.path, COALESCE(e.id, '')
		FROM library_sources ls LEFT JOIN entries e ON e.storage_id=ls.storage_id AND e.path=ls.path
		WHERE ls.library_id=? ORDER BY ls.id`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.LibrarySource
	for rows.Next() {
		var source model.LibrarySource
		if err := rows.Scan(&source.StorageID, &source.Path, &source.EntryID); err != nil {
			return nil, err
		}
		items = append(items, source)
	}
	return items, rows.Err()
}

func (c *Catalog) UpsertMediaFile(ctx context.Context, item model.MediaFile) error {
	if item.EntryID == "" || item.Kind == "" {
		return errors.New("media file entry ID and kind are required")
	}
	if len(item.Metadata) == 0 {
		item.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(item.Metadata) {
		return errors.New("media file metadata must be valid JSON")
	}
	item.UpdatedAt = time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `INSERT INTO media_files
		(entry_id, kind, duration_ms, container, width, height, video_codec, audio_codec, bitrate,
		 taken_at, camera, latitude, longitude, metadata, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(entry_id) DO UPDATE SET kind=excluded.kind, duration_ms=excluded.duration_ms,
			container=excluded.container, width=excluded.width, height=excluded.height,
			video_codec=excluded.video_codec, audio_codec=excluded.audio_codec,
			bitrate=excluded.bitrate, taken_at=excluded.taken_at, camera=excluded.camera,
			latitude=excluded.latitude, longitude=excluded.longitude,
			metadata=excluded.metadata, updated_at=excluded.updated_at`,
		item.EntryID, item.Kind, item.DurationMS, item.Container, item.Width, item.Height,
		item.VideoCodec, item.AudioCodec, item.Bitrate, item.TakenAt, item.Camera,
		item.Latitude, item.Longitude, string(item.Metadata), item.UpdatedAt)
	return err
}

func (c *Catalog) MediaFile(ctx context.Context, entryID string) (*model.MediaFile, error) {
	var item model.MediaFile
	var duration, bitrate sql.NullInt64
	var width, height sql.NullInt64
	var takenAt sql.NullTime
	var latitude, longitude sql.NullFloat64
	var metadata string
	err := c.reader.QueryRowContext(ctx, `SELECT entry_id, kind, duration_ms, container, width, height,
		video_codec, audio_codec, bitrate, taken_at, camera, latitude, longitude, metadata, updated_at FROM media_files WHERE entry_id=?`, entryID).
		Scan(&item.EntryID, &item.Kind, &duration, &item.Container, &width, &height,
			&item.VideoCodec, &item.AudioCodec, &bitrate, &takenAt, &item.Camera,
			&latitude, &longitude, &metadata, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if duration.Valid {
		item.DurationMS = &duration.Int64
	}
	if bitrate.Valid {
		item.Bitrate = &bitrate.Int64
	}
	if takenAt.Valid {
		item.TakenAt = &takenAt.Time
	}
	if latitude.Valid {
		item.Latitude = &latitude.Float64
	}
	if longitude.Valid {
		item.Longitude = &longitude.Float64
	}
	if width.Valid {
		value := int(width.Int64)
		item.Width = &value
	}
	if height.Valid {
		value := int(height.Int64)
		item.Height = &value
	}
	item.Metadata = json.RawMessage(metadata)
	return &item, nil
}

// EntriesMissingMediaAssociation pages through files whose extracted metadata
// can support a hierarchy but which have not yet received the requested role.
// It is used for small, versioned startup backfills without rescanning storage.
func (c *Catalog) EntriesMissingMediaAssociation(ctx context.Context, kind, role, afterID string, limit int) ([]model.Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := c.reader.QueryContext(ctx, `SELECT `+qualifiedEntryColumns+`
		FROM entries e JOIN media_files technical ON technical.entry_id=e.id
		WHERE technical.kind=? AND e.available=1 AND e.id>? AND
			(?!='album' OR trim(COALESCE(json_extract(technical.metadata, '$.music.album'), ''))!='') AND
			(?!='video' OR EXISTS (
				SELECT 1 FROM library_sources source JOIN libraries library ON library.id=source.library_id
				WHERE source.storage_id=e.storage_id AND library.type IN ('movies','tv') AND
					(source.path='' OR e.path=source.path OR substr(e.path,1,length(source.path)+1)=source.path || '/')
			)) AND
			NOT EXISTS (SELECT 1 FROM media_item_files association
				WHERE association.entry_id=e.id AND association.role=?)
		ORDER BY e.id LIMIT ?`, kind, afterID, role, kind, role, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Entry, 0, limit)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, entry)
	}
	return items, rows.Err()
}

// EntriesMissingAudioArtwork finds tagged audio that advertises embedded cover
// art but has no cached thumbnail yet. It lets a newly-added artwork processor
// backfill an existing catalog without walking the storage again.
func (c *Catalog) EntriesMissingAudioArtwork(ctx context.Context, afterID string, limit int) ([]model.Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := c.reader.QueryContext(ctx, `SELECT `+qualifiedEntryColumns+`
		FROM entries e JOIN media_files technical ON technical.entry_id=e.id
		WHERE technical.kind='audio' AND e.available=1 AND e.id>? AND
			json_extract(technical.metadata, '$.music.hasAlbumArt')=1 AND
			NOT EXISTS (SELECT 1 FROM artifacts artifact
				WHERE artifact.entry_id=e.id AND artifact.type='thumbnail')
		ORDER BY e.id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Entry, 0, limit)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, entry)
	}
	return items, rows.Err()
}

func (c *Catalog) UpsertArtifact(ctx context.Context, item model.Artifact) (*model.Artifact, error) {
	if item.ID == "" {
		item.ID = uuid.Must(uuid.NewV7()).String()
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.LastAccessedAt = now
	var entryID, mediaID any
	if item.EntryID != "" {
		entryID = item.EntryID
	}
	if item.MediaID != "" {
		mediaID = item.MediaID
	}
	err := c.db.QueryRowContext(ctx, `INSERT INTO artifacts
		(id, entry_id, media_id, type, variant, key, mime, size, created_at, last_accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(type, key) DO UPDATE SET entry_id=excluded.entry_id, media_id=excluded.media_id,
			variant=excluded.variant, mime=excluded.mime, size=excluded.size, last_accessed_at=excluded.last_accessed_at
		RETURNING id, COALESCE(entry_id, ''), COALESCE(media_id, ''), type, variant, key,
			COALESCE(mime, ''), size, created_at, last_accessed_at`,
		item.ID, entryID, mediaID, item.Type, item.Variant, item.Key, item.MIME, item.Size,
		item.CreatedAt, item.LastAccessedAt).Scan(&item.ID, &item.EntryID, &item.MediaID, &item.Type,
		&item.Variant, &item.Key, &item.MIME, &item.Size, &item.CreatedAt, &item.LastAccessedAt)
	return &item, err
}

func (c *Catalog) ArtifactForEntry(ctx context.Context, entryID, artifactType, variant string) (*model.Artifact, error) {
	var item model.Artifact
	err := c.reader.QueryRowContext(ctx, `SELECT id, COALESCE(entry_id, ''), COALESCE(media_id, ''), type,
		variant, key, COALESCE(mime, ''), size, created_at, last_accessed_at
		FROM artifacts WHERE entry_id=? AND type=? AND variant=? ORDER BY created_at DESC LIMIT 1`,
		entryID, artifactType, variant).Scan(&item.ID, &item.EntryID, &item.MediaID, &item.Type,
		&item.Variant, &item.Key, &item.MIME, &item.Size, &item.CreatedAt, &item.LastAccessedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

func (c *Catalog) ArtifactForMedia(ctx context.Context, mediaID, artifactType, variant string) (*model.Artifact, error) {
	var item model.Artifact
	err := c.reader.QueryRowContext(ctx, `SELECT id, COALESCE(entry_id, ''), COALESCE(media_id, ''), type,
		variant, key, COALESCE(mime, ''), size, created_at, last_accessed_at
		FROM artifacts WHERE media_id=? AND type=? AND variant=? ORDER BY created_at DESC LIMIT 1`,
		mediaID, artifactType, variant).Scan(&item.ID, &item.EntryID, &item.MediaID, &item.Type,
		&item.Variant, &item.Key, &item.MIME, &item.Size, &item.CreatedAt, &item.LastAccessedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

func (c *Catalog) TouchArtifact(ctx context.Context, id string) error {
	result, err := c.db.ExecContext(ctx, `UPDATE artifacts SET last_accessed_at=? WHERE id=?`, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}
