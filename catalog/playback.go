package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lsongdev/files-go/model"
)

func (c *Catalog) UpsertPlaybackState(ctx context.Context, state model.PlaybackState) (*model.PlaybackState, error) {
	if state.UserID == "" || state.MediaID == "" {
		return nil, errors.New("playback user and media IDs are required")
	}
	if state.PositionMS < 0 {
		return nil, errors.New("playback position cannot be negative")
	}
	state.UpdatedAt = time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `INSERT INTO playback_states(user_id,media_id,position_ms,played,updated_at)
		VALUES(?,?,?,?,?) ON CONFLICT(user_id,media_id) DO UPDATE SET position_ms=excluded.position_ms,
		played=excluded.played,updated_at=excluded.updated_at`, state.UserID, state.MediaID, state.PositionMS, state.Played, state.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c.PlaybackState(ctx, state.UserID, state.MediaID)
}

func (c *Catalog) PlaybackState(ctx context.Context, userID, mediaID string) (*model.PlaybackState, error) {
	var state model.PlaybackState
	var played int
	err := c.reader.QueryRowContext(ctx, `SELECT user_id,media_id,position_ms,played,updated_at FROM playback_states WHERE user_id=? AND media_id=?`, userID, mediaID).Scan(&state.UserID, &state.MediaID, &state.PositionMS, &played, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	state.Played = played != 0
	return &state, err
}

func (c *Catalog) ContinueWatching(ctx context.Context, userID string, limit int) ([]model.PlaybackState, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := c.reader.QueryContext(ctx, `SELECT p.user_id,p.media_id,p.position_ms,p.played,p.updated_at,
		m.id,m.type,m.title,m.sort_title,m.year,m.parent_id,m.index_number,m.external_id,m.match_source,
		m.match_confidence,m.match_locked,m.metadata,m.created_at,m.updated_at
		FROM playback_states p JOIN media_items m ON m.id=p.media_id
		WHERE p.user_id=? AND p.played=0 AND p.position_ms>0 ORDER BY p.updated_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.PlaybackState, 0)
	for rows.Next() {
		var state model.PlaybackState
		var played, locked int
		var item model.MediaItem
		var year, index sql.NullInt64
		var parent, external sql.NullString
		var metadata string
		if err := rows.Scan(&state.UserID, &state.MediaID, &state.PositionMS, &played, &state.UpdatedAt, &item.ID, &item.Type, &item.Title, &item.SortTitle, &year, &parent, &index, &external, &item.MatchSource, &item.MatchConfidence, &locked, &metadata, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		state.Played = played != 0
		item.MatchLocked = locked != 0
		item.Metadata = []byte(metadata)
		if year.Valid {
			value := int(year.Int64)
			item.Year = &value
		}
		if parent.Valid {
			item.ParentID = parent.String
		}
		if index.Valid {
			value := int(index.Int64)
			item.IndexNumber = &value
		}
		if external.Valid {
			item.ExternalID = external.String
		}
		state.Media = &item
		items = append(items, state)
	}
	return items, rows.Err()
}
