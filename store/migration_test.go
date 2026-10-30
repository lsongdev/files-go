package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationPreservesRowsCacheAndManualChoices(t *testing.T) {
	ctx := context.Background()
	name := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", name)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.ReplaceAll(schema, "INTEGER PRIMARY KEY AUTOINCREMENT", "INTEGER PRIMARY KEY")
	legacy = strings.ReplaceAll(legacy, "\trevision INTEGER NOT NULL DEFAULT 1,\n", "")
	legacy = strings.ReplaceAll(legacy, "CREATE TABLE overrides (root TEXT NOT NULL, path TEXT NOT NULL, choice TEXT NOT NULL, PRIMARY KEY(root, path));", "")
	if _, err := db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO files(id,root,path,name,dir,meta,meta_ver) VALUES
		(4,'r','Film','Film',1,'{"locked":true,"tmdb":"movie/123","title":"Film"}',7),
		(5,'r','Film/a.mkv','a.mkv',0,'{"title":"Video"}',7);
		INSERT INTO cache VALUES ('test',X'6869',0); PRAGMA user_version=1;`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := Open(name)
	if err != nil {
		t.Fatal(err)
	}
	f, err := st.Get(ctx, 5)
	if err != nil || f.Name != "a.mkv" || f.MetaVer != 0 || f.Revision != 1 {
		t.Fatalf("migrated file: %+v %v", f, err)
	}
	choice, err := st.Match(ctx, f)
	if err != nil || choice == nil || *choice != (Match{Provider: "tmdb", Type: "movie", ID: "123"}) {
		t.Fatalf("migrated choice: %+v %v", choice, err)
	}
	if b, _ := st.CacheGet(ctx, "test", 1<<62); string(b) != "hi" {
		t.Fatalf("cache lost: %q", b)
	}
	if found, err := st.Search(ctx, SearchOptions{Query: "Film"}); err != nil || len(found) != 1 {
		t.Fatalf("FTS migration: %+v %v", found, err)
	}
	st.Close()
	st, err = Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if choice, err := st.Match(ctx, f); err != nil || choice == nil {
		t.Fatalf("choice lost on reopen: %+v %v", choice, err)
	}
	if _, err := st.db.Exec(`DELETE FROM files WHERE id=5; INSERT INTO files(root,path,name) VALUES('r','Film/b.mkv','b.mkv')`); err != nil {
		t.Fatal(err)
	}
	replacement, _ := st.ByPath(ctx, "r", "Film/b.mkv")
	if replacement.ID <= 5 {
		t.Fatalf("ID reused after migration: %d", replacement.ID)
	}
}

func TestUnknownSchemaIsNotRebuilt(t *testing.T) {
	name := filepath.Join(t.TempDir(), "newer.db")
	st, err := Open(name)
	if err != nil {
		t.Fatal(err)
	}
	st.db.Exec(`INSERT INTO files(root,path,name) VALUES('r','keep','keep'); PRAGMA user_version=999`)
	st.Close()
	if newer, err := Open(name); err == nil {
		newer.Close()
		t.Fatal("accepted future schema")
	}
	db, err := sql.Open("sqlite", name)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM files WHERE name='keep'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("future database modified: %d %v", count, err)
	}
}
