package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lsongdev/files-go/model"
)

func (c *Catalog) UpsertPlaybackState(ctx context.Context, state model.PlaybackState) (*model.PlaybackState, error) {
	if state.UserID == "" || state.EntryID == "" {
		return nil, errors.New("playback user and entry IDs are required")
	}
	if state.PositionMS < 0 {
		return nil, errors.New("playback position cannot be negative")
	}
	state.UpdatedAt = time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `INSERT INTO playback_states(user_id,entry_id,position_ms,played,updated_at)
		VALUES(?,?,?,?,?) ON CONFLICT(user_id,entry_id) DO UPDATE SET position_ms=excluded.position_ms,
		played=excluded.played,updated_at=excluded.updated_at`, state.UserID, state.EntryID, state.PositionMS, state.Played, state.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c.PlaybackState(ctx, state.UserID, state.EntryID)
}

func (c *Catalog) PlaybackState(ctx context.Context, userID, entryID string) (*model.PlaybackState, error) {
	var state model.PlaybackState
	var played int
	err := c.reader.QueryRowContext(ctx, `SELECT user_id,entry_id,position_ms,played,updated_at FROM playback_states WHERE user_id=? AND entry_id=?`, userID, entryID).
		Scan(&state.UserID, &state.EntryID, &state.PositionMS, &played, &state.UpdatedAt)
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
	rows, err := c.reader.QueryContext(ctx, `SELECT p.user_id,p.entry_id,p.position_ms,p.played,p.updated_at
		FROM playback_states p JOIN entries e ON e.id=p.entry_id
		WHERE p.user_id=? AND p.played=0 AND p.position_ms>0 AND e.available=1
		ORDER BY p.updated_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.PlaybackState, 0)
	ids := make([]string, 0)
	for rows.Next() {
		var state model.PlaybackState
		var played int
		if err := rows.Scan(&state.UserID, &state.EntryID, &state.PositionMS, &played, &state.UpdatedAt); err != nil {
			return nil, err
		}
		state.Played = played != 0
		items = append(items, state)
		ids = append(ids, state.EntryID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	medias, err := c.MediasForEntries(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if media, ok := medias[items[i].EntryID]; ok {
			copy := media
			items[i].Media = &copy
		}
	}
	return items, nil
}
