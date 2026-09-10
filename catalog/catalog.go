package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lsongdev/files-go/model"
)

var ErrNotFound = errors.New("catalog entry not found")

type Catalog struct{ db *sql.DB }

func New(db *sql.DB) *Catalog { return &Catalog{db: db} }

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
	entry, err := scanEntry(c.db.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (c *Catalog) EntryByPath(ctx context.Context, storageID, path string) (*model.Entry, error) {
	entry, err := scanEntry(c.db.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE storage_id = ? AND path = ?`, storageID, path))
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
	rows, err := c.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE `+where+`
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

	rows, err := c.db.QueryContext(ctx, `SELECT `+qualifiedEntryColumns+`
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
	var generation int64
	err := c.db.QueryRowContext(ctx, `UPDATE storages SET state='scanning', updated_at=? WHERE id=? RETURNING scan_generation + 1`, time.Now().UTC(), storageID).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("storage %q is not registered", storageID)
	}
	return generation, err
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
	if _, err := tx.ExecContext(ctx, `UPDATE storages SET state='online', last_seen_at=?, last_scan_at=?, scan_generation=?, updated_at=? WHERE id=?`, now, now, generation, now, storageID); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Catalog) FailScan(ctx context.Context, storageID, state string) error {
	if state != "offline" {
		state = "error"
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE storages SET state=?, updated_at=? WHERE id=?`, state, now, storageID); err != nil {
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
	rows, err := c.db.QueryContext(ctx, `SELECT id, name, type, state, last_seen_at, last_scan_at, scan_generation FROM storages ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.Storage
	for rows.Next() {
		var item model.Storage
		var seen, scan sql.NullTime
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.State, &seen, &scan, &item.ScanGeneration); err != nil {
			return nil, err
		}
		if seen.Valid {
			item.LastSeenAt = &seen.Time
		}
		if scan.Valid {
			item.LastScanAt = &scan.Time
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (c *Catalog) Storage(ctx context.Context, id string) (*model.Storage, error) {
	var item model.Storage
	var seen, scan sql.NullTime
	err := c.db.QueryRowContext(ctx, `SELECT id, name, type, state, last_seen_at, last_scan_at, scan_generation FROM storages WHERE id=?`, id).
		Scan(&item.ID, &item.Name, &item.Type, &item.State, &seen, &scan, &item.ScanGeneration)
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
	return &item, nil
}

func (c *Catalog) Libraries(ctx context.Context) ([]model.Library, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id, name, type FROM libraries ORDER BY name, id`)
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
	rows, err := c.db.QueryContext(ctx, `SELECT ls.storage_id, ls.path, COALESCE(e.id, '')
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
