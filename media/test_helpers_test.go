package media

import (
	"context"
	"testing"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/model"
)

type fakeProvider struct{}

func (fakeProvider) Search(_ context.Context, query Query) ([]Candidate, error) {
	year := 2014
	if query.Year != nil { year = *query.Year }
	return []Candidate{{ID: "157336", Type: query.Type, Title: "Interstellar", OriginalTitle: "Interstellar", Year: &year}}, nil
}

func (fakeProvider) Fetch(_ context.Context, kind, id, _ string) (Candidate, error) {
	year := 2014
	return Candidate{ID: id, Type: kind, Title: "Interstellar", OriginalTitle: "Interstellar", Year: &year}, nil
}

func insertSidecarEntry(t *testing.T, cat *catalog.Catalog, generation int64, entry model.Entry) model.Entry {
	t.Helper()
	entries, err := cat.UpsertEntries(context.Background(), []model.Entry{entry}, generation)
	if err != nil { t.Fatal(err) }
	return entries[0]
}
