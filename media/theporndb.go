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

// JAV identifies only content explicitly configured with the jav media hint.
type JAV struct {
	Client *theporndb.Client
	Hint   func(store.File) (string, string)
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
func (j *JAV) Match(f *store.File) bool {
	hint, _ := j.Hint(*f)
	return hint == "jav" && (f.Kind == "video" || f.Dir)
}
func (j *JAV) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	if m.Locked || (!f.Dir && isExtra(f.Name)) {
		return nil
	}
	code := javCode(strings.TrimSuffix(f.Name, path.Ext(f.Name)))
	if code == "" && !f.Dir {
		code = javCode(path.Base(path.Dir(f.Path)))
	}
	if code == "" {
		return nil
	}
	m.Set("Code", code)
	if j.Client == nil {
		return nil
	}
	var scene *theporndb.SceneResource
	page, err := j.Client.SearchJAV(ctx, url.Values{"q": {code}, "per_page": {"50"}})
	if err != nil {
		return javError(err)
	}
	for _, candidate := range page.Data {
		sku := ""
		if candidate.SKU != nil {
			sku = *candidate.SKU
		}
		if javCode(candidate.Title) != code && javCode(sku) != code {
			continue
		}
		if scene != nil {
			m.Review = "ambiguous"
			return nil
		}
		copy := candidate
		scene = &copy
	}
	if scene != nil {
		detail, err := j.Client.GetJAV(ctx, scene.ID, nil)
		if err != nil {
			return javError(err)
		}
		scene = &detail.Data
	}
	if scene == nil {
		m.Review = "unmatched"
		return nil
	}
	m.Kind = "movie"
	m.Title, m.Summary, m.Year, m.Rating = scene.Title, scene.Description, leadingYear(scene.Date), scene.Rating
	m.ThePornDB = "jav/" + scene.ID
	if m.Duration == 0 && scene.Duration != nil {
		m.Duration = *scene.Duration
	}
	if scene.Poster != nil && *scene.Poster != "" {
		m.Poster = "theporndb:" + *scene.Poster
	}
	if scene.Background.Full != nil && *scene.Background.Full != "" {
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
	for _, tag := range scene.Tags {
		m.Genres = append(m.Genres, tag.Name)
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
