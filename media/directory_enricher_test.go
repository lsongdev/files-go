package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

func TestDirectoryEnricherStoresArtworkAndNFOOnDirectoryOnly(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "Arrival"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "Arrival", "movie.nfo"), []byte(`<movie><title>降临</title><year>2016</year></movie>`), 0644); err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocal(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", backend); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	root, err := cat.EnsureRoot(ctx, "disk", generation)
	if err != nil {
		t.Fatal(err)
	}
	directory := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &root.ID, Name: "Arrival", Path: "Arrival", Type: model.EntryDirectory})
	icon := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &directory.ID, Name: "folder.jpg", Path: "Arrival/folder.jpg", Type: model.EntryFile, Extension: "jpg"})
	backdrop := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &directory.ID, Name: "backdrop.jpg", Path: "Arrival/backdrop.jpg", Type: model.EntryFile, Extension: "jpg"})
	nfo := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &directory.ID, Name: "movie.nfo", Path: "Arrival/movie.nfo", Type: model.EntryFile, Extension: "nfo"})
	processor := NewDirectoryEnricher(cat, registry)
	if err := processor.Process(ctx, nfo); err != nil {
		t.Fatal(err)
	}
	item, err := cat.MediaForEntry(ctx, directory.ID)
	if err != nil || item.Title != "降临" || item.Icon != "file:"+icon.ID || item.Backdrop != "file:"+backdrop.ID || item.Year == nil || *item.Year != 2016 {
		t.Fatalf("directory media = %#v, %v", item, err)
	}
	if _, err := cat.MediaForEntry(ctx, icon.ID); err == nil {
		t.Fatal("folder.jpg unexpectedly gained movie enhancement")
	}
	for _, removed := range []model.Entry{icon, backdrop, nfo} {
		if err := cat.MarkEntryTreeUnavailable(ctx, removed); err != nil {
			t.Fatal(err)
		}
		if err := processor.Process(ctx, removed); err != nil {
			t.Fatal(err)
		}
	}
	if item, err := cat.MediaForEntry(ctx, directory.ID); item != nil || !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("removed sidecars left stale directory media: %#v, %v", item, err)
	}
}

func TestDirectoryEnricherMatchesOnlySingleMovieDirectories(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterLibrary(ctx, model.Library{ID: "movies", Name: "Movies", Type: "movies", Sources: []model.LibrarySource{{StorageID: "disk", Path: "Movies"}}}); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	root, err := cat.EnsureRoot(ctx, "disk", generation)
	if err != nil {
		t.Fatal(err)
	}
	library := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &root.ID, Name: "Movies", Path: "Movies", Type: model.EntryDirectory})
	folder := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &library.ID, Name: "Interstellar", Path: "Movies/Interstellar", Type: model.EntryDirectory})
	insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &folder.ID, Name: "Interstellar.2014.mkv", Path: "Movies/Interstellar/Interstellar.2014.mkv", Type: model.EntryFile, Extension: "mkv"})
	processor := NewDirectoryEnricherWithProvider(cat, storage.NewRegistry(), fakeProvider{}, "zh-CN")
	if err := processor.ProcessDirectory(ctx, library); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.MediaForEntry(ctx, library.ID); err == nil {
		t.Fatal("library root gained a movie identity")
	}
	if err := processor.ProcessDirectory(ctx, folder); err != nil {
		t.Fatal(err)
	}
	item, err := cat.MediaForEntry(ctx, folder.ID)
	if err != nil || item.Kind != "movie" || item.Title != "Interstellar" {
		t.Fatalf("single movie folder = %#v, %v", item, err)
	}
	insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &folder.ID, Name: "Arrival.2016.mkv", Path: "Movies/Interstellar/Arrival.2016.mkv", Type: model.EntryFile, Extension: "mkv"})
	if err := processor.ProcessDirectory(ctx, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.MediaForEntry(ctx, folder.ID); err == nil {
		t.Fatal("mixed movie folder retained a movie identity")
	}
}

type tvDirectoryProvider struct{}

func (tvDirectoryProvider) Search(_ context.Context, query Query) ([]Candidate, error) {
	return []Candidate{{ID: "tv-1", Type: query.Type, Title: "Better Call Saul"}}, nil
}

func (tvDirectoryProvider) Fetch(_ context.Context, kind, id, _ string) (Candidate, error) {
	return Candidate{ID: id, Type: kind, Title: "Better Call Saul"}, nil
}

func TestDirectoryEnricherInfersTVShowFromSeasonEpisodes(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterLibrary(ctx, model.Library{ID: "tv", Name: "TV", Type: "tv", Sources: []model.LibrarySource{{StorageID: "disk", Path: "TV"}}}); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	root, err := cat.EnsureRoot(ctx, "disk", generation)
	if err != nil {
		t.Fatal(err)
	}
	library := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &root.ID, Name: "TV", Path: "TV", Type: model.EntryDirectory})
	show := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &library.ID, Name: "Better.Call.Saul", Path: "TV/Better.Call.Saul", Type: model.EntryDirectory})
	season := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &show.ID, Name: "S01", Path: "TV/Better.Call.Saul/S01", Type: model.EntryDirectory})
	episode := insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &season.ID, Name: "Better.Call.Saul.S01E01.mkv", Path: "TV/Better.Call.Saul/S01/Better.Call.Saul.S01E01.mkv", Type: model.EntryFile, Extension: "mkv"})
	processor := NewDirectoryEnricherWithProvider(cat, storage.NewRegistry(), tvDirectoryProvider{}, "zh-CN")
	if err := processor.Process(ctx, episode); err != nil {
		t.Fatal(err)
	}
	item, err := cat.MediaForEntry(ctx, show.ID)
	if err != nil || item.Kind != "tv" || item.Title != "Better Call Saul" {
		t.Fatalf("TV directory media = %#v, %v", item, err)
	}
	if _, err := cat.MediaForEntry(ctx, season.ID); err == nil {
		t.Fatal("season directory unexpectedly gained TV show identity")
	}
	insertSidecarEntry(t, cat, generation, model.Entry{StorageID: "disk", ParentID: &season.ID, Name: "Different.Show.S01E02.mkv", Path: "TV/Better.Call.Saul/S01/Different.Show.S01E02.mkv", Type: model.EntryFile, Extension: "mkv"})
	if err := processor.ProcessDirectory(ctx, show); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.MediaForEntry(ctx, show.ID); err == nil {
		t.Fatal("mixed TV folder retained show identity")
	}
}

func TestEpisodeBelongsToSeriesWithBilingualFilename(t *testing.T) {
	entry := model.Entry{Name: "纸牌屋.House.Of.Cards.2013.S01E01.mkv", Path: "TV/House.Of.Cards/S01/纸牌屋.House.Of.Cards.2013.S01E01.mkv", Type: model.EntryFile, Extension: "mkv"}
	if !episodeBelongsToSeries(entry, "House Of Cards") {
		t.Fatal("bilingual episode must support the show name embedded in the parsed title")
	}
	if episodeBelongsToSeries(entry, "Better Call Saul") {
		t.Fatal("unrelated series was accepted")
	}
}
