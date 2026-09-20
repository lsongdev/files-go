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
	plugins := []processor.Plugin{
		Image(nil, nil, thumbnail), Ebook(nil, nil, thumbnail),
		Document(nil, nil, thumbnail, "", "", ""), Audio(nil, nil, thumbnail, "", ""),
		Video(nil, nil, thumbnail, nil, nil, "", ""), Sidecar(mediaengine.NewDirectoryEnricher(nil, nil)),
	}
	tests := []struct {
		file string
		want []string
	}{
		{"photo.JPG", []string{"image"}},
		{"folder.jpg", []string{"image", "sidecar"}},
		{"book.epub", []string{"ebook"}},
		{"paper.pdf", []string{"document"}},
		{"song.mp3", []string{"audio"}},
		{"movie.mkv", []string{"video", "sidecar"}},
		{"tvshow.nfo", []string{"sidecar"}},
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
	video := plugins[4]
	var steps []string
	for _, step := range video.Steps() {
		steps = append(steps, step.Name())
	}
	if want := []string{"ffprobe", "video_thumbnail"}; !slices.Equal(steps, want) {
		t.Errorf("video steps = %v, want %v", steps, want)
	}
}
