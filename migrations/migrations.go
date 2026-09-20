package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var files embed.FS

func Apply(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	entries, err := files.ReadDir(".")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return fmt.Errorf("invalid migration name %q", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return fmt.Errorf("invalid migration name %q: %w", name, err)
		}
		var applied int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied); err != nil {
			return err
		}
		if applied != 0 {
			continue
		}
		script, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(script)); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, version)
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	// The squashed init is intentionally not a migration path for databases that
	// stopped partway through the old 001–020 sequence. Reject those instead of
	// silently treating their recorded 001 as the current schema.
	for _, check := range []struct {
		query string
		want  int
	}{
		{`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('storages', 'libraries', 'library_sources', 'entries', 'entry_search', 'jobs', 'scan_checkpoints', 'medias')`, 8},
		{`SELECT COUNT(*) FROM pragma_table_info('storages') WHERE name='scan_scope'`, 1},
		{`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('media_files', 'media_items', 'media_item_files', 'media_match_suppressions')`, 0},
	} {
		var count int
		if err := db.QueryRowContext(ctx, check.query).Scan(&count); err != nil {
			return err
		}
		if count != check.want {
			return fmt.Errorf("database schema is incomplete or from an unsupported pre-squash migration; rebuild the database from configured libraries")
		}
	}
	return nil
}
