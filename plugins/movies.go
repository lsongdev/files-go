package plugins

import (
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/processor"
	"github.com/lsongdev/files-go/tmdb"
)

// Movies composes semantic movie/TV capabilities. Generic video probing stays
// in the video plugin; TMDB transport and artwork stay in the tmdb package.
func Movies(file *processor.MovieFile, directory *processor.MovieDirectory, artwork *tmdb.Artwork) Plugin {
	steps := []processor.Processor{file}
	if artwork != nil {
		directory.SetMatchedHook(artwork.ProcessEntry)
		steps = append(steps, artwork)
	}
	steps = append(steps, directory)
	return NewPlugin("movies", func(entry model.Entry) bool {
		return file.Match(entry) || directory.Match(entry)
	}, steps...)
}
