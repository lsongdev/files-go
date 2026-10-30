package media

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/lsongdev/files-go/pkg/theporndb"
	"github.com/lsongdev/files-go/store"
)

// JAV identifies only content explicitly configured with the jav media hint,
// through ThePornDB and local JavBus records (see javbus.go).
type JAV struct {
	Client *theporndb.Client
	Hint   func(store.File) (string, string)
	Store  *store.Store
	Open   Opener // reads JavBus records; nil skips them
	gate   breaker
}

var javCodePattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])([a-z]{2,10})[-_ ]?([0-9]{2,6})(?:[^0-9]|$)`)

func javCode(name string) string {
	m := javCodePattern.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	n, _ := strconv.Atoi(m[2])
	return fmt.Sprintf("%s-%03d", strings.ToUpper(m[1]), n)
}
func (j *JAV) Match(f *store.File) bool { return !f.Dir && f.Kind == "video" }
func (j *JAV) Enrich(ctx context.Context, f *store.File, m *Meta) (err error) {
	if f.Dir || isExtra(f.Name) || (m.Match != nil && m.Match.Provider != "theporndb") {
		return nil
	}
	hint := "jav"
	if j.Hint != nil {
		hint, _ = j.Hint(*f)
	}
	if hint != "jav" && !m.Locked {
		return nil
	}
	code := javCode(strings.TrimSuffix(f.Name, path.Ext(f.Name)))
	if code == "" {
		code = javCode(path.Base(path.Dir(f.Path)))
	}
	record, err := readJavBus(ctx, j.Store, j.Open, f, code)
	if err != nil {
		return err
	}
	if code == "" && record != nil {
		code = javCode(record.ID)
	}
	if code == "" && !m.Locked {
		return nil
	}
	m.Kind, m.Title = "movie", code
	m.Set("Code", code)
	if record != nil {
		record.apply(m)
	}
	if j.Client == nil {
		return nil
	}
	if err = j.gate.before(ctx); err != nil {
		return err
	}
	defer func() { j.gate.after(err) }()
	var scene *theporndb.SceneResource
	if m.Locked && m.Match != nil {
		detail, err := j.Client.GetJAV(ctx, m.Match.ID, nil)
		if err != nil {
			return javError(err)
		}
		scene = &detail.Data
	} else {
		page, err := j.Client.SearchJAV(ctx, url.Values{"q": {code}, "per_page": {"50"}})
		if err != nil {
			return javError(err)
		}
		var same []theporndb.SceneResource
		for _, candidate := range page.Data {
			sku := ""
			if candidate.SKU != nil {
				sku = *candidate.SKU
			}
			if javCode(candidate.Title) == code || javCode(sku) == code {
				same = append(same, candidate)
			}
		}
		if len(same) > 1 && record != nil && record.ReleaseDate != "" {
			// Catalogs list re-releases under the same code; the local
			// record's release date tells which one this is.
			var dated []theporndb.SceneResource
			for _, c := range same {
				if c.Date == record.ReleaseDate {
					dated = append(dated, c)
				}
			}
			if len(dated) > 0 {
				same = dated
			}
		}
		switch {
		case len(same) > 1:
			m.Review = "ambiguous"
			return nil
		case len(same) == 0 && record != nil:
			return nil // described by the local record alone
		case len(same) == 0:
			m.Review = "unmatched"
			return nil
		}
		scene = &same[0]
		detail, err := j.Client.GetJAV(ctx, scene.ID, nil)
		if err != nil {
			return javError(err)
		}
		scene = &detail.Data
	}
	m.Title, m.Summary, m.Year, m.Rating = scene.Title, scene.Description, leadingYear(scene.Date), scene.Rating
	m.setMatch("theporndb", "jav", scene.ID)
	if m.Duration == 0 && scene.Duration != nil {
		m.Duration = *scene.Duration
	}
	if scene.Poster != nil && *scene.Poster != "" && (m.Poster == "" || m.Poster == "self") {
		m.Poster = "theporndb:" + *scene.Poster
	}
	if scene.Background.Full != nil && *scene.Background.Full != "" && m.Backdrop == "" {
		m.Backdrop = "theporndb:" + *scene.Background.Full
	}
	if scene.Site != nil {
		m.Set("Studio", scene.Site.Name)
	}
	var performers []string
	for _, p := range scene.Performers {
		performers = append(performers, p.Name)
	}
	m.Set("Performers", strings.Join(performers, ", "))
	if len(scene.Tags) > 0 {
		m.Genres = nil
	}
	for _, tag := range scene.Tags {
		m.Genres = append(m.Genres, tag.Name)
	}
	if record != nil {
		record.supplement(m)
	}
	if j.Store != nil {
		return (&Library{Store: j.Store}).artwork(ctx, f.Parent, m)
	}
	return nil
}
func javError(err error) error {
	var api *theporndb.Error
	if errors.As(err, &api) && api.StatusCode != 429 && api.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%w: %v", ErrTransient, err)
}
