package catalog

import (
	"context"
	"time"
)

func (c *Catalog) MaintenanceCompleted(ctx context.Context, name string) (bool, error) {
	var count int
	err := c.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM maintenance_tasks WHERE name=?`, name).Scan(&count)
	return count != 0, err
}

func (c *Catalog) CompleteMaintenance(ctx context.Context, name string) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO maintenance_tasks(name, completed_at) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET completed_at=excluded.completed_at`, name, time.Now().UTC())
	return err
}
