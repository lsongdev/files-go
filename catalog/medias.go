package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lsongdev/files-go/model"
)

// MediaCandidate is one source's complete set of proposed fields. Empty
// fields do not erase a nonempty field proposed by another source.
type MediaCandidate struct {
	Kind     string          `json:"kind,omitempty"`
	Title    string          `json:"title,omitempty"`
	Icon     string          `json:"icon,omitempty"`
	Backdrop string          `json:"backdrop,omitempty"`
	Year     *int            `json:"year,omitempty"`
	Line1    string          `json:"line1,omitempty"`
	Line2    string          `json:"line2,omitempty"`
	Line3    string          `json:"line3,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// The ordering is deliberately shared by every plugin. Source payloads are
// kept so removal of an NFO or manual override can reveal the next candidate.
var mediaSourcePriority = []string{
	"manual", "local_nfo", "local_artwork", "embedded", "tmdb", "legacy", "filename", "screenshot",
}

func validMediaSource(source string) bool {
	for _, candidate := range mediaSourcePriority {
		if source == candidate {
			return true
		}
	}
	return false
}

func validMediaReference(ref string) bool {
	return ref == "" || strings.HasPrefix(ref, "file:") && len(ref) > len("file:") ||
		strings.HasPrefix(ref, "cache:") && len(ref) > len("cache:") ||
		strings.HasPrefix(ref, "legacy-poster:") && len(ref) > len("legacy-poster:")
}

// SetMediaCandidate atomically replaces one source's proposal and resolves
// the single display row. It never mutates another source's candidate.
func (c *Catalog) SetMediaCandidate(ctx context.Context, fileID, source string, candidate MediaCandidate) (*model.Media, error) {
	if !validMediaSource(source) || !validMediaReference(candidate.Icon) || !validMediaReference(candidate.Backdrop) ||
		len(candidate.Data) > 0 && !json.Valid(candidate.Data) {
		return nil, errors.New("invalid media candidate")
	}
	return c.changeMediaCandidate(ctx, fileID, source, &candidate)
}

func (c *Catalog) ClearMediaCandidate(ctx context.Context, fileID, source string) (*model.Media, error) {
	if !validMediaSource(source) {
		return nil, errors.New("invalid media source")
	}
	return c.changeMediaCandidate(ctx, fileID, source, nil)
}

func (c *Catalog) changeMediaCandidate(ctx context.Context, fileID, source string, candidate *MediaCandidate) (*model.Media, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var name string
	var existing sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT e.name, m.sources FROM entries e LEFT JOIN medias m ON m.file_id=e.id WHERE e.id=?`, fileID).Scan(&name, &existing)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sources := map[string]MediaCandidate{}
	if existing.Valid && existing.String != "" {
		if err := json.Unmarshal([]byte(existing.String), &sources); err != nil {
			return nil, fmt.Errorf("decode media sources: %w", err)
		}
	}
	if candidate == nil {
		delete(sources, source)
	} else {
		sources[source] = *candidate
	}
	if len(sources) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM medias WHERE file_id=?`, fileID); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	item := resolveMedia(fileID, name, sources)
	encoded, err := json.Marshal(sources)
	if err != nil {
		return nil, err
	}
	item.Sources = encoded
	item.UpdatedAt = time.Now().UTC()
	var year any
	if item.Year != nil {
		year = *item.Year
	}
	locked := 0
	if item.MatchLocked {
		locked = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO medias
		(file_id, kind, title, icon, backdrop, year, line1, line2, line3, data, sources, match_locked, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(file_id) DO UPDATE SET kind=excluded.kind, title=excluded.title,
		icon=excluded.icon, backdrop=excluded.backdrop, year=excluded.year,
		line1=excluded.line1, line2=excluded.line2, line3=excluded.line3,
		data=excluded.data, sources=excluded.sources, match_locked=excluded.match_locked,
		updated_at=excluded.updated_at`, fileID, item.Kind, item.Title, item.Icon, item.Backdrop,
		year, item.Line1, item.Line2, item.Line3, string(item.Data), string(item.Sources), locked, item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &item, nil
}

func resolveMedia(fileID, name string, sources map[string]MediaCandidate) model.Media {
	item := model.Media{FileID: fileID, Title: name, Data: json.RawMessage(`{}`)}
	data := map[string]json.RawMessage{}
	titleSelected := false
	for _, source := range mediaSourcePriority {
		candidate, ok := sources[source]
		if !ok {
			continue
		}
		if source == "manual" {
			item.MatchLocked = true
		}
		if item.Kind == "" && candidate.Kind != "" {
			item.Kind = candidate.Kind
		}
		if !titleSelected && candidate.Title != "" {
			item.Title = candidate.Title
			titleSelected = true
		}
		if item.Icon == "" && candidate.Icon != "" {
			item.Icon = candidate.Icon
		}
		if item.Backdrop == "" && candidate.Backdrop != "" {
			item.Backdrop = candidate.Backdrop
		}
		if item.Year == nil && candidate.Year != nil {
			year := *candidate.Year
			item.Year = &year
		}
		if item.Line1 == "" && candidate.Line1 != "" {
			item.Line1 = candidate.Line1
		}
		if item.Line2 == "" && candidate.Line2 != "" {
			item.Line2 = candidate.Line2
		}
		if item.Line3 == "" && candidate.Line3 != "" {
			item.Line3 = candidate.Line3
		}
		if len(candidate.Data) > 0 {
			data[source] = candidate.Data
		}
	}
	if len(data) > 0 {
		item.Data, _ = json.Marshal(data)
	}
	return item
}

func scanMedia(row scanner) (model.Media, error) {
	var item model.Media
	var year sql.NullInt64
	var data, sources string
	var locked int
	err := row.Scan(&item.FileID, &item.Kind, &item.Title, &item.Icon, &item.Backdrop,
		&year, &item.Line1, &item.Line2, &item.Line3, &data, &sources, &locked, &item.UpdatedAt)
	if year.Valid {
		value := int(year.Int64)
		item.Year = &value
	}
	item.Data = json.RawMessage(data)
	item.Sources = json.RawMessage(sources)
	item.MatchLocked = locked != 0
	return item, err
}

const mediaColumns = `file_id, kind, title, icon, backdrop, year, line1, line2, line3, data, sources, match_locked, updated_at`

func (c *Catalog) MediaForEntry(ctx context.Context, fileID string) (*model.Media, error) {
	item, err := scanMedia(c.reader.QueryRowContext(ctx, `SELECT `+mediaColumns+` FROM medias WHERE file_id=?`, fileID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &item, err
}

func (c *Catalog) MediasForEntries(ctx context.Context, fileIDs []string) (map[string]model.Media, error) {
	result := make(map[string]model.Media)
	if len(fileIDs) == 0 {
		return result, nil
	}
	if len(fileIDs) > 500 {
		return nil, errors.New("media batch exceeds 500 entries")
	}
	args := make([]any, len(fileIDs))
	for i, id := range fileIDs {
		args[i] = id
	}
	rows, err := c.reader.QueryContext(ctx, `SELECT `+mediaColumns+` FROM medias WHERE file_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(fileIDs)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		result[item.FileID] = item
	}
	return result, rows.Err()
}

func (c *Catalog) IsLibrarySourceRoot(ctx context.Context, entry model.Entry) (bool, error) {
	var count int
	err := c.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM library_sources WHERE storage_id=? AND path=?`, entry.StorageID, entry.Path).Scan(&count)
	return count > 0, err
}
