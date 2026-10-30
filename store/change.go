package store

// Change describes committed index updates for live views. Listeners get
// hints, not data: they re-read what they show through the usual queries.
type Change struct {
	Dirs  []int64 // directories whose listing changed: entries added, removed or updated
	Files []int64 // rows whose own derived metadata changed

	// A directory reconciled with the filesystem, for activity feeds.
	Root, Path              string
	Added, Removed, Updated int
}

type changeListener struct{ fn func(Change) }

// OnChange registers the listener for committed changes. It is called after
// commit on the writer's goroutine, so it must not block.
func (s *Store) OnChange(fn func(Change)) { s.listener.Store(&changeListener{fn}) }

func (s *Store) changed(c Change) {
	if l := s.listener.Load(); l != nil && (len(c.Dirs) > 0 || len(c.Files) > 0) {
		l.fn(c)
	}
}
