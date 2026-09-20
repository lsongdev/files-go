package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "catalog.db")+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInitSchema(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration count=%d err=%v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('medias') WHERE name='summary'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("media summary column count=%d err=%v", count, err)
	}
	for _, name := range []string{"media_files", "media_items", "media_item_files", "media_match_suppressions", "artifacts", "maintenance_tasks", "playback_states"} {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil || count != 0 {
			t.Fatalf("legacy table %s count=%d err=%v", name, count, err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO storages(id,name,type,created_at,updated_at) VALUES ('disk','Disk','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO entries(id,storage_id,name,path,type,created_at,updated_at) VALUES ('entry','disk','Movie.mkv','Movie.mkv','file',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO medias(file_id,kind,title,updated_at) VALUES ('entry','movie','Movie',CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entry_search WHERE entry_search MATCH 'Movie'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("search insert count=%d err=%v", count, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE entries SET name='Renamed.mkv' WHERE id='entry'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entry_search WHERE entry_search MATCH 'Renamed'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("search update count=%d err=%v", count, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM entries WHERE id='entry'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"entry_search", "medias"} {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}

func TestCompletedOldHistoryCanReopen(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	for version := 3; version <= 20; version++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if err := Apply(ctx, db); err != nil {
		t.Fatalf("reopen complete old history: %v", err)
	}
}

func TestIncompleteOldHistoryIsRejected(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY); INSERT INTO schema_migrations(version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("expected incomplete schema error, got %v", err)
	}
}
