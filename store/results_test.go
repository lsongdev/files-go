package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestResultsRejectChangedAndRecreatedFiles(t *testing.T) {
	ctx := context.Background()
	st, scanner, root := setup(t)
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	f, _ := st.ByPath(ctx, "r", "notes.txt")
	old := Result{ID: f.ID, Revision: f.Revision, Kind: f.Kind, Version: 1, Meta: []byte(`{"title":"old"}`)}
	write(t, filepath.Join(root, "notes.txt"), "changed content")
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveResults(ctx, []Result{old}); !errors.Is(err, ErrStale) {
		t.Fatalf("old save: %v", err)
	}
	fresh, _ := st.Get(ctx, f.ID)
	if fresh.MetaVer != 0 || len(fresh.Meta) != 0 {
		t.Fatalf("old result overwrote change: %+v", fresh)
	}
	os.Remove(filepath.Join(root, "notes.txt"))
	scanner.Scan(ctx, "r", false)
	write(t, filepath.Join(root, "notes.txt"), "replacement")
	scanner.Scan(ctx, "r", false)
	replacement, _ := st.ByPath(ctx, "r", "notes.txt")
	if replacement.ID == f.ID {
		t.Fatal("deleted ID reused")
	}
	if err := st.SaveResults(ctx, []Result{old}); !errors.Is(err, ErrStale) {
		t.Fatalf("deleted save: %v", err)
	}
}

func TestMatchesPersistAndInvalidateScope(t *testing.T) {
	ctx := context.Background()
	st, scanner, _ := setup(t)
	scanner.Scan(ctx, "r", false)
	dir, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)")
	f, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)/Dune.2021.mkv")
	choice := &Match{Provider: "theporndb", Type: "jav", ID: "abc"}
	if err := st.SetMatch(ctx, dir, choice); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Match(ctx, f); err != nil || got == nil || *got != *choice {
		t.Fatalf("inherited match: %+v %v", got, err)
	}
	if err := st.SaveResults(ctx, []Result{{ID: f.ID, Revision: f.Revision, Version: 1}}); !errors.Is(err, ErrStale) {
		t.Fatalf("worker overwrote match: %v", err)
	}
	if err := st.SetMatch(ctx, dir, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Match(ctx, f); err != nil || got != nil {
		t.Fatalf("unlock: %+v %v", got, err)
	}
}

func TestResultsPropagateOnlyChangedOutput(t *testing.T) {
	ctx := context.Background()
	st, scanner, _ := setup(t)
	scanner.Scan(ctx, "r", false)
	f, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)/Dune.2021.mkv")
	parent, _ := st.Get(ctx, f.Parent)
	result := Result{ID: f.ID, Revision: f.Revision, Kind: f.Kind, Version: 1, Meta: []byte(`{"title":"Film"}`)}
	if err := st.SaveResults(ctx, []Result{result}); err != nil {
		t.Fatal(err)
	}
	after, _ := st.Get(ctx, parent.ID)
	if after.Revision <= parent.Revision || after.MetaVer != 0 {
		t.Fatalf("parent was not invalidated: %+v", after)
	}
	if err := st.SaveResults(ctx, []Result{result}); err != nil {
		t.Fatal(err)
	}
	again, _ := st.Get(ctx, parent.ID)
	if again.Revision != after.Revision {
		t.Fatal("identical output invalidated parent again")
	}
	// A stale directory result and a good file result can share one batch.
	result.Meta = []byte(`{"title":"Changed"}`)
	err := st.SaveResults(ctx, []Result{result, {ID: parent.ID, Revision: parent.Revision, Kind: "folder", Version: 1}})
	if !errors.Is(err, ErrStale) {
		t.Fatalf("mixed batch: %v", err)
	}
	updated, _ := st.Get(ctx, f.ID)
	if string(updated.Meta) != string(result.Meta) {
		t.Fatal("good result rolled back with stale result")
	}
}

func TestMatchPrecedenceAndRootRemoval(t *testing.T) {
	ctx := context.Background()
	st, scanner, _ := setup(t)
	scanner.Scan(ctx, "r", false)
	dir, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)")
	f, _ := st.ByPath(ctx, "r", "Movies/Dune (2021)/Dune.2021.mkv")
	group := &Match{Provider: "tmdb", Type: "movie", ID: "1"}
	file := &Match{Provider: "theporndb", Type: "jav", ID: "abc"}
	if err := st.SetMatch(ctx, dir, group); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMatch(ctx, f, file); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Match(ctx, f); err != nil || got == nil || *got != *file {
		t.Fatalf("specific choice: %+v %v", got, err)
	}
	if err := NewScanner(st, nil, nil, nil).Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := scanner.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	fresh, _ := st.ByPath(ctx, "r", f.Path)
	if fresh.ID == f.ID {
		t.Fatal("reindex reused ID")
	}
	if got, err := st.Match(ctx, fresh); err != nil || got == nil || *got != *file {
		t.Fatalf("choice lost with removed root: %+v %v", got, err)
	}
	if err := st.SetMatch(ctx, fresh, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Match(ctx, fresh); err != nil || got == nil || *got != *group {
		t.Fatalf("parent choice not restored: %+v %v", got, err)
	}
}

func TestLibraryListsEachWorkOnce(t *testing.T) {
	ctx := context.Background()
	st, scanner, root := setup(t)
	write(t, filepath.Join(root, "Movies", "Arrival.mkv"), "video")
	write(t, filepath.Join(root, "Movies", "Dune (2021)", "Dune.2021.part2.mkv"), "video")
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	save := func(path, kind, title string) {
		t.Helper()
		f, err := st.ByPath(ctx, "r", path)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SaveResults(ctx, []Result{{ID: f.ID, Revision: f.Revision, Kind: f.Kind, Version: 1, Title: title, Meta: []byte(`{"kind":"` + kind + `"}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	save("Movies/Dune (2021)", "movie", "Dune")
	save("Movies/Dune (2021)/Dune.2021.mkv", "movie", "Dune")
	save("Movies/Dune (2021)/Dune.2021.part2.mkv", "movie", "Dune")
	save("Movies/Arrival.mkv", "movie", "") // no title: sorted by name
	save("Movies", "album", "Zzz")          // a parent of another kind does not hide
	save("notes.txt", "book", "Notes")

	names := func(opts LibraryOptions) []string {
		t.Helper()
		if opts.Limit == 0 {
			opts.Limit = 10
		}
		files, err := st.Library(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, f := range files {
			out = append(out, f.Name)
		}
		return out
	}
	if got := names(LibraryOptions{Kind: "movie"}); !slices.Equal(got, []string{"Arrival.mkv", "Dune (2021)"}) {
		t.Fatalf("by title = %v", got)
	}
	if got := names(LibraryOptions{Kind: "movie", Limit: 1, Offset: 1}); !slices.Equal(got, []string{"Dune (2021)"}) {
		t.Fatalf("second page = %v", got)
	}
	if got := names(LibraryOptions{Kind: "movie", Root: "other"}); len(got) != 0 {
		t.Fatalf("other root = %v", got)
	}
	if got := names(LibraryOptions{Kind: "show"}); len(got) != 0 {
		t.Fatalf("no shows = %v", got)
	}
	recent := filepath.Join(root, "Movies", "Arrival.mkv")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(recent, future, future); err != nil {
		t.Fatal(err)
	}
	if err := scanner.Scan(ctx, "r", false); err != nil {
		t.Fatal(err)
	}
	save("Movies/Arrival.mkv", "movie", "")
	if got := names(LibraryOptions{Kind: "movie", Recent: true}); len(got) != 2 || got[0] != "Arrival.mkv" {
		t.Fatalf("recent = %v", got)
	}
	// The kind filter and default order must come from the index, with no sort.
	rows, err := st.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM files f WHERE json_extract(f.meta, '$.kind')='movie'
		ORDER BY COALESCE(NULLIF(f.title, ''), f.name) COLLATE NOCASE, f.id LIMIT 10`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "files_library") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("plan:\n%s", plan)
	}
}
