package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
)

func TestScanProgressPersistsAndInterruptedScanDoesNotNeedInitialScan(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	needsScan, err := cat.NeedsInitialScan(ctx, "disk")
	if err != nil || !needsScan {
		t.Fatalf("empty storage needs scan = %v, %v", needsScan, err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.EnsureRoot(ctx, "disk", generation); err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateScanProgress(ctx, "disk", 42, 35, 7); err != nil {
		t.Fatal(err)
	}
	if err := cat.RecoverInterruptedScans(ctx); err != nil {
		t.Fatal(err)
	}
	storage, err := cat.Storage(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	if storage.State != "interrupted" || storage.ScanEntries != 42 || storage.ScanFiles != 35 || storage.ScanDirectories != 7 || storage.ScanStartedAt == nil || storage.ScanUpdatedAt == nil {
		t.Fatalf("recovered storage = %#v", storage)
	}
	needsScan, err = cat.NeedsInitialScan(ctx, "disk")
	if err != nil || needsScan {
		t.Fatalf("partial catalog needs initial scan = %v, %v", needsScan, err)
	}
}

func TestCanceledScanIsRecoveredAsInterrupted(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.BeginScan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	if err := cat.FailScan(ctx, "disk", "interrupted", context.Canceled.Error()); err != nil {
		t.Fatal(err)
	}
	storage, err := cat.Storage(ctx, "disk")
	if err != nil || storage.State != "interrupted" {
		t.Fatalf("storage = %#v, %v", storage, err)
	}

	if _, err := db.ExecContext(ctx, `UPDATE storages SET state='error', scan_error='context canceled' WHERE id='disk'`); err != nil {
		t.Fatal(err)
	}
	if err := cat.RecoverInterruptedScans(ctx); err != nil {
		t.Fatal(err)
	}
	storage, err = cat.Storage(ctx, "disk")
	if err != nil || storage.State != "interrupted" {
		t.Fatalf("recovered storage = %#v, %v", storage, err)
	}
}

func TestRescanUsesExistingEntryCountAsProgressEstimate(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, _ := cat.BeginScan(ctx, "disk")
	if _, err := cat.EnsureRoot(ctx, "disk", generation); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "one", Path: "one", Type: model.EntryFile}}, generation); err != nil {
		t.Fatal(err)
	}
	if err := cat.CompleteScan(ctx, "disk", generation); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.BeginScan(ctx, "disk"); err != nil {
		t.Fatal(err)
	}
	storage, err := cat.Storage(ctx, "disk")
	if err != nil || storage.ScanEstimate != 2 {
		t.Fatalf("scan estimate = %#v, %v", storage, err)
	}
}

func TestSearchTracksEntriesAndFiltersLibraries(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
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
	root, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Movies", Path: "Movies", Type: model.EntryDirectory}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	parentID := root[0].ID
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", ParentID: &parentID, Name: "Interstellar.mkv", Path: "Movies/Interstellar.mkv", Type: model.EntryFile, Extension: "mkv"}}, generation)
	if err != nil {
		t.Fatal(err)
	}

	results, err := cat.Search(ctx, SearchOptions{Query: "inter", LibraryID: "movies", Type: model.EntryFile, Extension: ".MKV"})
	if err != nil || len(results) != 1 || results[0].ID != entries[0].ID {
		t.Fatalf("initial search = %#v, %v", results, err)
	}
	results, err = cat.Search(ctx, SearchOptions{Query: "inter", LibraryID: "music"})
	if err != nil || len(results) != 0 {
		t.Fatalf("library-filtered search = %#v, %v", results, err)
	}

	renamed := entries[0]
	renamed.Name = "星际穿越.mkv"
	if _, err := cat.UpsertEntries(ctx, []model.Entry{renamed}, generation); err != nil {
		t.Fatal(err)
	}
	results, err = cat.Search(ctx, SearchOptions{Query: "星际"})
	if err != nil || len(results) != 1 || results[0].Name != renamed.Name {
		t.Fatalf("renamed search = %#v, %v", results, err)
	}
	results, err = cat.Search(ctx, SearchOptions{Query: "inter"})
	if err != nil || len(results) != 0 {
		t.Fatalf("stale name search = %#v, %v", results, err)
	}
}

func TestMediaFilesAndArtifactsRemainSeparateFromEntries(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "movie.mkv", Path: "movie.mkv", Type: model.EntryFile}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	duration, width, height, bitrate := int64(123400), 1920, 1080, int64(8_000_000)
	takenAt := time.Date(2024, time.May, 6, 7, 8, 9, 0, time.UTC)
	latitude, longitude := 31.2304, 121.4737
	media := model.MediaFile{EntryID: entries[0].ID, Kind: "video", DurationMS: &duration, Container: "matroska", Width: &width, Height: &height, VideoCodec: "h264", AudioCodec: "aac", Bitrate: &bitrate, TakenAt: &takenAt, Camera: "Test Camera", Latitude: &latitude, Longitude: &longitude, Metadata: json.RawMessage(`{"streams":2}`)}
	if err := cat.UpsertMediaFile(ctx, media); err != nil {
		t.Fatal(err)
	}
	stored, err := cat.MediaFile(ctx, entries[0].ID)
	if err != nil || stored.Kind != "video" || stored.Width == nil || *stored.Width != width || stored.TakenAt == nil || !stored.TakenAt.Equal(takenAt) || stored.Camera != "Test Camera" || stored.Latitude == nil || *stored.Latitude != latitude || stored.Longitude == nil || *stored.Longitude != longitude || string(stored.Metadata) != string(media.Metadata) {
		t.Fatalf("media file = %#v, %v", stored, err)
	}

	artifact, err := cat.UpsertArtifact(ctx, model.Artifact{EntryID: entries[0].ID, Type: "thumbnail", Variant: "medium", Key: "abc-medium", MIME: "image/jpeg", Size: 42})
	if err != nil {
		t.Fatal(err)
	}
	found, err := cat.ArtifactForEntry(ctx, entries[0].ID, "thumbnail", "medium")
	if err != nil || found.ID != artifact.ID || found.Key != "abc-medium" {
		t.Fatalf("artifact = %#v, %v", found, err)
	}
	previousCreated := found.CreatedAt
	artifact.Size = 84
	updated, err := cat.UpsertArtifact(ctx, *artifact)
	if err != nil || updated.ID != artifact.ID || updated.Size != 84 || !updated.CreatedAt.Equal(previousCreated) {
		t.Fatalf("updated artifact = %#v, %v", updated, err)
	}
}

func TestMediaItemsAssociateFilesAndSupportManualUnmatch(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Interstellar.mkv", Path: "Interstellar.mkv", Type: model.EntryFile}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	year := 2014
	item, err := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "movie", Title: "Interstellar", SortTitle: "interstellar", Year: &year, ExternalID: "tmdb:157336", MatchSource: "manual", MatchConfidence: 1, MatchLocked: true, Metadata: json.RawMessage(`{"overview":"Space"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AssociateMediaFile(ctx, item.ID, entries[0].ID, "video"); err != nil {
		t.Fatal(err)
	}
	found, err := cat.MediaItemForEntry(ctx, entries[0].ID, "video")
	if err != nil || found.ID != item.ID || !found.MatchLocked || found.Year == nil || *found.Year != year {
		t.Fatalf("associated media item = %#v, %v", found, err)
	}
	loaded, err := cat.MediaItem(ctx, item.ID)
	if err != nil || len(loaded.Files) != 1 || loaded.Files[0].EntryID != entries[0].ID {
		t.Fatalf("media files = %#v, %v", loaded, err)
	}
	summaries, err := cat.MediaSummariesForEntries(ctx, []string{entries[0].ID, "missing"})
	summary, exists := summaries[entries[0].ID]
	if err != nil || !exists || summary.ID != item.ID || summary.Title != "Interstellar" || summary.Year == nil || *summary.Year != year {
		t.Fatalf("media summaries = %#v, %v", summaries, err)
	}
	if err := cat.UnmatchEntry(ctx, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.MediaItemForEntry(ctx, entries[0].ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unmatch result = %v", err)
	}
}

func TestEntryMediaPrefersPlayableItemOverAudioParents(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{StorageID: "disk", Name: "Song.mp3", Path: "Song.mp3", Type: model.EntryFile}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	artist, _ := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "artist", Title: "Artist"})
	album, _ := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "album", Title: "Album", ParentID: artist.ID})
	track, _ := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "track", Title: "Song", ParentID: album.ID})
	for _, association := range []struct {
		item *model.MediaItem
		role string
	}{{track, "audio"}, {artist, "artist"}, {album, "album"}} {
		if err := cat.AssociateMediaFile(ctx, association.item.ID, entries[0].ID, association.role); err != nil {
			t.Fatal(err)
		}
	}
	item, err := cat.MediaItemForEntry(ctx, entries[0].ID, "")
	if err != nil || item.ID != track.ID {
		t.Fatalf("default media item = %#v, %v; want track", item, err)
	}
	summaries, err := cat.MediaSummariesForEntries(ctx, []string{entries[0].ID})
	if err != nil || summaries[entries[0].ID].ID != track.ID {
		t.Fatalf("media summary = %#v, %v; want track", summaries, err)
	}
	item, err = cat.MediaItemForEntry(ctx, entries[0].ID, "album")
	if err != nil || item.ID != album.ID {
		t.Fatalf("explicit album item = %#v, %v", item, err)
	}
}

func TestEntriesMissingAudioArtworkOnlyReturnsTaggedAudioWithoutThumbnail(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, _ := cat.BeginScan(ctx, "disk")
	entries, err := cat.UpsertEntries(ctx, []model.Entry{
		{StorageID: "disk", Name: "with-art.mp3", Path: "with-art.mp3", Type: model.EntryFile},
		{StorageID: "disk", Name: "without-art.mp3", Path: "without-art.mp3", Type: model.EntryFile},
	}, generation)
	if err != nil {
		t.Fatal(err)
	}
	for index, metadata := range []string{`{"music":{"hasAlbumArt":true}}`, `{"music":{"hasAlbumArt":false}}`} {
		if err := cat.UpsertMediaFile(ctx, model.MediaFile{EntryID: entries[index].ID, Kind: "audio", Metadata: json.RawMessage(metadata)}); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := cat.EntriesMissingAudioArtwork(ctx, "", 100)
	if err != nil || len(missing) != 1 || missing[0].ID != entries[0].ID {
		t.Fatalf("missing audio artwork = %#v, %v", missing, err)
	}
	if _, err := cat.UpsertArtifact(ctx, model.Artifact{EntryID: entries[0].ID, Type: "thumbnail", Variant: "medium", Key: "cover", MIME: "image/jpeg"}); err != nil {
		t.Fatal(err)
	}
	missing, err = cat.EntriesMissingAudioArtwork(ctx, "", 100)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing audio artwork after thumbnail = %#v, %v", missing, err)
	}
}

func TestEntriesNeedingEpisodeMetadataSkipsTMDBAndLockedEpisodes(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterLibrary(ctx, model.Library{ID: "tv", Name: "TV", Type: "tv", Sources: []model.LibrarySource{{StorageID: "disk", Path: "TV"}}}); err != nil {
		t.Fatal(err)
	}
	generation, _ := cat.BeginScan(ctx, "disk")
	entries, err := cat.UpsertEntries(ctx, []model.Entry{
		{StorageID: "disk", Name: "Show.S01E01.mkv", Path: "TV/Show.S01E01.mkv", Type: model.EntryFile},
		{StorageID: "disk", Name: "Show.S01E02.mkv", Path: "TV/Show.S01E02.mkv", Type: model.EntryFile},
		{StorageID: "disk", Name: "Show.S01E03.mkv", Path: "TV/Show.S01E03.mkv", Type: model.EntryFile},
	}, generation)
	if err != nil {
		t.Fatal(err)
	}
	items := []model.MediaItem{
		{Type: "episode", Title: "Episode 1", MatchSource: "filename"},
		{Type: "episode", Title: "Episode 2", MatchSource: "tmdb"},
		{Type: "episode", Title: "Episode 3", MatchSource: "filename", MatchLocked: true},
	}
	for index := range items {
		item, err := cat.UpsertMediaItem(ctx, items[index])
		if err != nil {
			t.Fatal(err)
		}
		if err := cat.AssociateMediaFile(ctx, item.ID, entries[index].ID, "video"); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := cat.EntriesNeedingEpisodeMetadata(ctx, "", 100)
	if err != nil || len(missing) != 1 || missing[0].ID != entries[0].ID {
		t.Fatalf("episode metadata backfill = %#v, %v", missing, err)
	}
}

func TestDirectoryMediaContextRequiresOneCoherentIdentity(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := New(db)
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
	directories, err := cat.UpsertEntries(ctx, []model.Entry{
		{StorageID: "disk", ParentID: &root.ID, Name: "Movie A", Path: "Movie A", Type: model.EntryDirectory},
		{StorageID: "disk", ParentID: &root.ID, Name: "Movie B", Path: "Movie B", Type: model.EntryDirectory},
	}, generation)
	if err != nil {
		t.Fatal(err)
	}
	files, err := cat.UpsertEntries(ctx, []model.Entry{
		{StorageID: "disk", ParentID: &directories[0].ID, Name: "a.mkv", Path: "Movie A/a.mkv", Type: model.EntryFile},
		{StorageID: "disk", ParentID: &directories[1].ID, Name: "b.mkv", Path: "Movie B/b.mkv", Type: model.EntryFile},
	}, generation)
	if err != nil {
		t.Fatal(err)
	}
	for index, title := range []string{"Movie A", "Movie B"} {
		item, err := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "movie", Title: title, MatchSource: "filename"})
		if err != nil {
			t.Fatal(err)
		}
		if err := cat.AssociateMediaFile(ctx, item.ID, files[index].ID, "video"); err != nil {
			t.Fatal(err)
		}
	}
	item, err := cat.MediaItemForDirectory(ctx, directories[0])
	if err != nil || item.Title != "Movie A" || item.PrimaryEntryID != files[0].ID {
		t.Fatalf("directory media = %#v, %v", item, err)
	}
	if _, err := cat.MediaItemForDirectory(ctx, *root); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mixed root media error = %v, want not found", err)
	}
	providerMatch, err := cat.UpsertMediaItem(ctx, model.MediaItem{Type: "movie", Title: "Movie A (TMDB)", MatchSource: "tmdb"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AssociateMediaFile(ctx, providerMatch.ID, files[0].ID, "video"); err != nil {
		t.Fatal(err)
	}
	best, err := cat.DirectMovieForDirectory(ctx, directories[0])
	if err != nil || best.ID != providerMatch.ID {
		t.Fatalf("direct movie = %#v, %v", best, err)
	}
	if _, err := cat.DirectMovieForDirectory(ctx, *root); !errors.Is(err, ErrNotFound) {
		t.Fatalf("library root direct movie error = %v, want not found", err)
	}
}
