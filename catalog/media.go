package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lsongdev/files-go/model"
)

func (c *Catalog) UpsertMediaItem(ctx context.Context, item model.MediaItem) (*model.MediaItem, error) {
	if strings.TrimSpace(item.Type) == "" || strings.TrimSpace(item.Title) == "" {
		return nil, errors.New("media item type and title are required")
	}
	if item.ID == "" {
		item.ID = uuid.Must(uuid.NewV7()).String()
	}
	if len(item.Metadata) == 0 {
		item.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(item.Metadata) {
		return nil, errors.New("media item metadata must be valid JSON")
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	var year, parentID, indexNumber, externalID any
	if item.Year != nil {
		year = *item.Year
	}
	if item.ParentID != "" {
		parentID = item.ParentID
	}
	if item.IndexNumber != nil {
		indexNumber = *item.IndexNumber
	}
	if item.ExternalID != "" {
		externalID = item.ExternalID
	}
	_, err := c.db.ExecContext(ctx, `INSERT INTO media_items
		(id, type, title, sort_title, year, parent_id, index_number, external_id, match_source,
		 match_confidence, match_locked, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET type=excluded.type, title=excluded.title,
		sort_title=excluded.sort_title, year=excluded.year, parent_id=excluded.parent_id,
		index_number=excluded.index_number, external_id=excluded.external_id,
		match_source=excluded.match_source, match_confidence=excluded.match_confidence,
		match_locked=excluded.match_locked, metadata=excluded.metadata, updated_at=excluded.updated_at`,
		item.ID, item.Type, item.Title, item.SortTitle, year, parentID, indexNumber, externalID,
		item.MatchSource, item.MatchConfidence, item.MatchLocked, string(item.Metadata), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c.MediaItem(ctx, item.ID)
}

func (c *Catalog) MediaItem(ctx context.Context, id string) (*model.MediaItem, error) {
	item, err := scanMediaItem(c.reader.QueryRowContext(ctx, `SELECT id, type, title, sort_title,
		year, parent_id, index_number, external_id, match_source, match_confidence, match_locked,
		metadata, created_at, updated_at FROM media_items WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	files, err := c.MediaItemFiles(ctx, id)
	if err != nil {
		return nil, err
	}
	item.Files = files
	return &item, nil
}

func (c *Catalog) MediaItemByExternalID(ctx context.Context, itemType, externalID string) (*model.MediaItem, error) {
	item, err := scanMediaItem(c.reader.QueryRowContext(ctx, `SELECT id, type, title, sort_title,
		year, parent_id, index_number, external_id, match_source, match_confidence, match_locked,
		metadata, created_at, updated_at FROM media_items WHERE type=? AND external_id=?`, itemType, externalID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

func (c *Catalog) MediaItems(ctx context.Context, itemType, libraryID string, limit int) ([]model.MediaItem, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	where, args := []string{}, []any{}
	if itemType != "" {
		where = append(where, "m.type=?")
		args = append(args, itemType)
	}
	if libraryID != "" {
		where = append(where, `EXISTS (SELECT 1 FROM media_item_files mf JOIN entries e ON e.id=mf.entry_id
			JOIN library_sources ls ON ls.storage_id=e.storage_id
			WHERE mf.media_id=m.id AND ls.library_id=? AND
			(ls.path='' OR e.path=ls.path OR substr(e.path,1,length(ls.path)+1)=ls.path || '/'))`)
		args = append(args, libraryID)
	}
	args = append(args, limit)
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}
	rows, err := c.reader.QueryContext(ctx, `SELECT m.id, m.type, m.title, m.sort_title, m.year, m.parent_id,
		m.index_number, m.external_id, m.match_source, m.match_confidence, m.match_locked, m.metadata,
		m.created_at, m.updated_at, COALESCE((SELECT mf.entry_id FROM media_item_files mf
		WHERE mf.media_id=m.id ORDER BY mf.role, mf.entry_id LIMIT 1), '')
		FROM media_items m `+whereSQL+` ORDER BY m.sort_title, m.title, m.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.MediaItem, 0)
	for rows.Next() {
		item, err := scanMediaItemWithPrimary(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (c *Catalog) MediaChild(ctx context.Context, parentID, itemType string, indexNumber int) (*model.MediaItem, error) {
	item, err := scanMediaItem(c.reader.QueryRowContext(ctx, `SELECT id, type, title, sort_title,
		year, parent_id, index_number, external_id, match_source, match_confidence, match_locked,
		metadata, created_at, updated_at FROM media_items
		WHERE parent_id=? AND type=? AND index_number=? LIMIT 1`, parentID, itemType, indexNumber))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

func (c *Catalog) LibraryTypesForEntry(ctx context.Context, entry model.Entry) ([]string, error) {
	rows, err := c.reader.QueryContext(ctx, `SELECT DISTINCT l.type FROM library_sources ls
		JOIN libraries l ON l.id=ls.library_id
		WHERE ls.storage_id=? AND (ls.path='' OR ?=ls.path OR substr(?, 1, length(ls.path)+1)=ls.path || '/')
		ORDER BY l.type`, entry.StorageID, entry.Path, entry.Path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		types = append(types, value)
	}
	return types, rows.Err()
}

func (c *Catalog) AssociateMediaFile(ctx context.Context, mediaID, entryID, role string) error {
	if mediaID == "" || entryID == "" || role == "" {
		return errors.New("media ID, entry ID, and role are required")
	}
	_, err := c.db.ExecContext(ctx, `INSERT INTO media_item_files(media_id, entry_id, role, created_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(media_id, entry_id, role) DO NOTHING`, mediaID, entryID, role, time.Now().UTC())
	return err
}

func (c *Catalog) MediaItemForEntry(ctx context.Context, entryID, role string) (*model.MediaItem, error) {
	where, args := "mf.entry_id=?", []any{entryID}
	if role != "" {
		where += " AND mf.role=?"
		args = append(args, role)
	}
	item, err := scanMediaItem(c.reader.QueryRowContext(ctx, `SELECT m.id, m.type, m.title, m.sort_title,
		m.year, m.parent_id, m.index_number, m.external_id, m.match_source, m.match_confidence,
		m.match_locked, m.metadata, m.created_at, m.updated_at FROM media_item_files mf
		JOIN media_items m ON m.id=mf.media_id WHERE `+where+` ORDER BY mf.created_at DESC LIMIT 1`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

func (c *Catalog) MediaItemFiles(ctx context.Context, mediaID string) ([]model.MediaItemFile, error) {
	rows, err := c.reader.QueryContext(ctx, `SELECT media_id, entry_id, role, created_at
		FROM media_item_files WHERE media_id=? ORDER BY role, entry_id`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.MediaItemFile, 0)
	for rows.Next() {
		var item model.MediaItemFile
		if err := rows.Scan(&item.MediaID, &item.EntryID, &item.Role, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (c *Catalog) UnmatchEntry(ctx context.Context, entryID string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM media_item_files WHERE entry_id=?`, entryID)
	return err
}

func (c *Catalog) SetMediaMatchLocked(ctx context.Context, id string, locked bool) error {
	result, err := c.db.ExecContext(ctx, `UPDATE media_items SET match_locked=?, updated_at=? WHERE id=?`, locked, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return ErrNotFound
	}
	return nil
}

func scanMediaItem(row scanner) (model.MediaItem, error) {
	return scanMediaItemValue(row, false)
}

func scanMediaItemWithPrimary(row scanner) (model.MediaItem, error) {
	return scanMediaItemValue(row, true)
}

func scanMediaItemValue(row scanner, withPrimary bool) (model.MediaItem, error) {
	var item model.MediaItem
	var year, indexNumber sql.NullInt64
	var parentID, externalID sql.NullString
	var locked int
	var metadata string
	targets := []any{&item.ID, &item.Type, &item.Title, &item.SortTitle, &year, &parentID,
		&indexNumber, &externalID, &item.MatchSource, &item.MatchConfidence, &locked,
		&metadata, &item.CreatedAt, &item.UpdatedAt}
	if withPrimary {
		targets = append(targets, &item.PrimaryEntryID)
	}
	err := row.Scan(targets...)
	if year.Valid {
		value := int(year.Int64)
		item.Year = &value
	}
	if indexNumber.Valid {
		value := int(indexNumber.Int64)
		item.IndexNumber = &value
	}
	if parentID.Valid {
		item.ParentID = parentID.String
	}
	if externalID.Valid {
		item.ExternalID = externalID.String
	}
	item.MatchLocked = locked != 0
	item.Metadata = json.RawMessage(metadata)
	return item, err
}
