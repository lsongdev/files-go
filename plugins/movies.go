package plugins

import (
	mediaengine "github.com/lsongdev/files-go/media"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
)

// Movies owns semantic movie/TV enrichment: filename matching, TMDB artwork,
// NFO files and local folder artwork. Technical video probing stays in Video.
func Movies(video *mediaengine.VideoEnricher, directory *mediaengine.DirectoryEnricher, artwork *mediaengine.Artwork) Plugin {
	return NewPlugin("movies", func(entry model.Entry) bool {
		return video.Match(entry) || directory.Match(entry)
	}, video, artwork, directory)
}
