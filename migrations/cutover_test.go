package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func TestFileCentricCutoverDiscardsSeededLegacyMedia(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "catalog.db")+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	entries, err := files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name() < "020_file_centric_cutover.sql" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		script, err := files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(script)); err != nil {
			t.Fatalf("apply historical migration %s: %v", name, err)
		}
		prefix, _, _ := strings.Cut(name, "_")
		version, _ := strconv.Atoi(prefix)
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO storages(id,name,type,state,created_at,updated_at) VALUES ('disk','Disk','local','online',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO entries(id,storage_id,name,path,type,created_at,updated_at) VALUES ('entry','disk','Movie.mkv','Movie.mkv','file',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO media_items(id,type,title,created_at,updated_at) VALUES ('old-movie','movie','Old Movie',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO media_item_files(media_id,entry_id,role,created_at) VALUES ('old-movie','entry','primary',CURRENT_TIMESTAMP)`,
		`INSERT INTO media_files(entry_id,kind,updated_at) VALUES ('entry','video',CURRENT_TIMESTAMP)`,
		`INSERT INTO playback_states(user_id,media_id,updated_at) VALUES ('local','old-movie',CURRENT_TIMESTAMP)`,
		`INSERT INTO artifacts(id,entry_id,media_id,type,key,created_at,last_accessed_at) VALUES ('art','entry','old-movie','poster','old-key',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO medias(file_id,kind,title,updated_at) VALUES ('entry','movie','Transitional',CURRENT_TIMESTAMP)`,
		`INSERT INTO jobs(id,type,state,created_at) VALUES ('job','process_entry','pending',CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed old schema: %v", err)
		}
	}
	if err := Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	for _, name := range []string{"media_files", "media_items", "media_item_files", "media_match_suppressions"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil || count != 0 {
			t.Fatalf("legacy table %s count=%d err=%v", name, count, err)
		}
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM medias`,
		`SELECT COUNT(*) FROM jobs WHERE type='process_entry'`,
		`SELECT COUNT(*) FROM pragma_table_info('artifacts') WHERE name='media_id'`,
		`SELECT COUNT(*) FROM pragma_table_info('playback_states') WHERE name='media_id'`,
	} {
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: count=%d err=%v", query, count, err)
		}
	}
	var entryIDColumn int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('playback_states') WHERE name='entry_id'`).Scan(&entryIDColumn); err != nil || entryIDColumn != 1 {
		t.Fatalf("playback entry_id count=%d err=%v", entryIDColumn, err)
	}
}
