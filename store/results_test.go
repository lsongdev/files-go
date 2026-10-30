package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
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
