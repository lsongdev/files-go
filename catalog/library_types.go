package catalog

import (
	"context"

	"github.com/lsongdev/files-go/model"
)

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
