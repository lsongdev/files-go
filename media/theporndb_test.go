package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/lsongdev/files-go/pkg/theporndb"
	"github.com/lsongdev/files-go/store"
)

func TestJAVCode(t *testing.T) {
	for input, want := range map[string]string{"CAWD-549-C.mp4": "CAWD-549", "cawd00549": "CAWD-549", "[site] SONE-405.mp4": "SONE-405", "MVSD": "", "1080p.mp4": "", "xCAWD-549": "XCAWD-549"} {
		if got := javCode(input); got != want {
			t.Errorf("%s: %s, want %s", input, got, want)
		}
	}
}
func TestJAVMatching(t *testing.T) {
	for _, tc := range []struct {
		name, results, review string
		matched               bool
	}{
		{"exact", `[{"id":"yes","title":"CAWD-549 - title"},{"id":"near","title":"CAWD-545 - title"}]`, "", true},
		{"near", `[{"id":"near","title":"CAWD-545 - title"}]`, "unmatched", false},
		{"ambiguous", `[{"id":"one","title":"CAWD-549 - title"},{"id":"two","title":"CAWD-549 - other"}]`, "ambiguous", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/jav" {
					if r.URL.Query().Get("q") != "CAWD-549" {
						t.Error("wrong code")
					}
					w.Write([]byte(`{"data":` + tc.results + `}`))
					return
				}
				if r.URL.Path != "/jav/yes" {
					t.Error("incorrect detail lookup")
				}
				w.Write([]byte(`{"data":{"id":"yes","title":"Full title","date":"2024-01-02","duration":120,"poster":"https://thumb.theporndb.net/a.jpg"}}`))
			}))
			defer s.Close()
			c, _ := theporndb.New(theporndb.Options{Token: "test", BaseURL: s.URL})
			j := &JAV{Client: c, Hint: func(store.File) (string, string) { return "jav", "" }}
			f := store.File{Name: "CAWD-549-C.mp4", Kind: "video"}
			m := &Meta{Duration: 100}
			if !j.Match(&f) {
				t.Fatal("should match")
			}
			if err := j.Enrich(context.Background(), &f, m); err != nil {
				t.Fatal(err)
			}
			if m.Review != tc.review || (m.providerID("theporndb") != "") != tc.matched {
				t.Fatalf("unexpected result: %+v", m)
			}
			if tc.matched && (m.Duration != 100 || m.Year != 2024 || m.Poster == "") {
				t.Fatalf("metadata not applied: %+v", m)
			}
		})
	}
}
func TestJAVLive(t *testing.T) {
	token := os.Getenv("THEPORNDB_TOKEN")
	if token == "" {
		t.Skip("THEPORNDB_TOKEN not set")
	}
	c, err := theporndb.New(theporndb.Options{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	j := &JAV{Client: c}
	m := &Meta{}
	if err := j.Enrich(context.Background(), &store.File{Name: "CAWD-549.mp4", Kind: "video"}, m); err != nil {
		t.Fatal(err)
	}
	if m.providerID("theporndb") == "" {
		t.Fatalf("not matched: %+v", m)
	}
}
