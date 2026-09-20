package catalog

import (
	"context"

	"github.com/lsongdev/files-go/model"
)

// StaleDirectoryEvidence returns one removed media-relevant child per parent
// before a completed scan marks its old generation unavailable. Recalculating
// each affected parent once is sufficient even if many child files vanished.
func (c *Catalog) StaleDirectoryEvidence(ctx context.Context, storageID string, generation int64) ([]model.Entry, error) {
	rows, err := c.reader.QueryContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE id IN (
		SELECT MIN(id) FROM entries
		WHERE storage_id=? AND scan_generation<? AND available=1 AND parent_id IS NOT NULL
		AND (type='directory' OR lower(extension) IN
			('nfo','jpg','jpeg','png','mp4','m4v','mkv','webm','mov','avi','mpeg','mpg','ts','m2ts','wmv','rmvb'))
		GROUP BY parent_id
	) ORDER BY path`, storageID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.Entry, 0)
	for rows.Next() {
		item, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
