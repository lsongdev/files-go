package enrichment

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
)

func TestMediaPluginFileMatching(t *testing.T) {
	thumbnail := processor.NewThumbnail(nil, nil, "")
	directory := mediaengine.NewDirectoryEnricher(nil, nil)
	plugins := []processor.Plugin{
		Image(nil, nil, thumbnail),
		Video(nil, nil, thumbnail, "", ""),
		Movies(mediaengine.NewVideoEnricher(nil, nil, ""), directory, mediaengine.NewArtwork(nil, "", nil)),
		Music(nil, nil, thumbnail, "", ""),
		Ebook(nil, nil, thumbnail),
		Document(nil, nil, thumbnail, "", "", ""),
	}
	tests := []struct {
		file string
		want []string
	}{
		{"photo.JPG", []string{"image"}},
		{"folder.jpg", []string{"image", "movies"}},
		{"book.epub", []string{"ebook"}},
		{"paper.pdf", []string{"document"}},
		{"song.mp3", []string{"music"}},
		{"movie.mkv", []string{"video", "movies"}},
		{"tvshow.nfo", []string{"movies"}},
		{"._movie.mkv", nil},
		{"some.d.ts", nil},
	}
	for _, test := range tests {
		entry := model.Entry{Name: test.file, Extension: strings.TrimPrefix(filepath.Ext(test.file), "."), Type: model.EntryFile}
		var got []string
		for _, plugin := range plugins {
			if plugin.Match(entry) {
				got = append(got, plugin.Name())
			}
		}
		if !slices.Equal(got, test.want) {
			t.Errorf("%s matched %v, want %v", test.file, got, test.want)
		}
	}
	var videoSteps []string
	for _, step := range plugins[1].Steps() {
		videoSteps = append(videoSteps, step.Name())
	}
	if want := []string{"ffprobe", "video_thumbnail"}; !slices.Equal(videoSteps, want) {
		t.Errorf("video steps = %v, want %v", videoSteps, want)
	}
	var movieSteps []string
	for _, step := range plugins[2].Steps() {
		movieSteps = append(movieSteps, step.Name())
	}
	if want := []string{"video_enrichment", "tmdb_artwork", "directory_enrichment"}; !slices.Equal(movieSteps, want) {
		t.Errorf("movies steps = %v, want %v", movieSteps, want)
	}
}
