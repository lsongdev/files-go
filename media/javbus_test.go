package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/pkg/theporndb"
	"github.com/lsongdev/files-go/store"
)

func TestJavBusRecords(t *testing.T) {
	const record = `{"id":"CAWD-549","title":"CAWD-549 Local title","release_date":"2024-01-02","length":"120min",
		"director":"Director","studio":"Studio","label":"Label","series":"Series",
		"genre":["Drama","Star A"],"stars":[{"name":"Star A"},{"name":"Star B"}]}`
	for _, tc := range []struct {
		name, json, results, review, title, match string
	}{
		// Re-releases share a code; the record's date picks the right one.
		{"dated", record, `[{"id":"old","title":"CAWD-549 a","date":"2020-05-05"},{"id":"yes","title":"CAWD-549 b","date":"2024-01-02"}]`, "", "Full title", "yes"},
		{"ambiguous without a date", `{"id":"CAWD-549","title":"CAWD-549 x"}`, `[{"id":"old","title":"CAWD-549 a","date":"2020-05-05"},{"id":"yes","title":"CAWD-549 b","date":"2024-01-02"}]`, "ambiguous", "x", ""},
		// Unknown to the catalog: the record describes it, nothing to review.
		{"local", record, `[]`, "", "Local title", ""},
		{"another code", `{"id":"CAWD-550","title":"CAWD-550 other"}`, `[]`, "unmatched", "CAWD-549", ""},
		{"not a record", `{"files":[]}`, `[]`, "unmatched", "CAWD-549", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/jav":
					w.Write([]byte(`{"data":` + tc.results + `}`))
				case "/jav/yes":
					w.Write([]byte(`{"data":{"id":"yes","title":"Full title","date":"2024-01-02","tags":[{"name":"Tag"}]}}`))
				default:
					t.Errorf("unexpected request %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer api.Close()
			root := t.TempDir()
			os.MkdirAll(filepath.Join(root, "CAWD-549"), 0755)
			os.WriteFile(filepath.Join(root, "CAWD-549", "CAWD-549-C.mp4"), []byte("x"), 0600)
			os.WriteFile(filepath.Join(root, "CAWD-549", "CAWD-549.json"), []byte(tc.json), 0600)
			st, err := store.Open(filepath.Join(t.TempDir(), "files.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			scanner := store.NewScanner(st, []config.Root{{ID: "r", Path: root, Type: "jav"}}, nil, Classify)
			if err := scanner.Scan(ctx, "r", false); err != nil {
				t.Fatal(err)
			}
			client, _ := theporndb.New(theporndb.Options{Token: "test", BaseURL: api.URL})
			j := &JAV{Client: client, Store: st, Open: scanner.Open, Hint: func(store.File) (string, string) { return "jav", "" }}
			f, _ := st.ByPath(ctx, "r", "CAWD-549/CAWD-549-C.mp4")
			m := &Meta{}
			if err := j.Enrich(ctx, &f, m); err != nil {
				t.Fatal(err)
			}
			if m.Review != tc.review || m.Title != tc.title || (m.Match == nil) != (tc.match == "") || m.Match != nil && m.Match.ID != tc.match {
				t.Fatalf("result: %+v", m)
			}
			switch tc.name {
			case "dated":
				if m.Info["Director"] != "Director" || m.Info["Label"] != "Label" || len(m.Genres) != 1 || m.Genres[0] != "Tag" {
					t.Errorf("catalog result not supplemented: %+v", m)
				}
			case "local":
				if m.Year != 2024 || m.Duration != 7200 || m.Info["Performers"] != "Star A, Star B" || len(m.Genres) != 1 || m.Genres[0] != "Drama" || m.Info["Code"] != "CAWD-549" {
					t.Errorf("record not applied: %+v", m)
				}
			}
		})
	}
}
