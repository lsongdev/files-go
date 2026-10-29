// Code generated from ThePornDB OpenAPI v3.24.769; DO NOT EDIT.
package theporndb

import (
	"context"
	"fmt"
	"net/url"
)

// SearchScenes List/Search Scenes.
func (c *Client) SearchScenes(ctx context.Context, query url.Values) (*Page[SceneResource], error) {
	var result Page[SceneResource]
	if err := c.get(ctx, "/scenes", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SearchJAV List/Search Jav.
func (c *Client) SearchJAV(ctx context.Context, query url.Values) (*Page[SceneResource], error) {
	var result Page[SceneResource]
	if err := c.get(ctx, "/jav", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SearchMovies List/Search Movies.
func (c *Client) SearchMovies(ctx context.Context, query url.Values) (*Page[SceneResource], error) {
	var result Page[SceneResource]
	if err := c.get(ctx, "/movies", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPerformerScenes Get Performer Scenes.
func (c *Client) GetPerformerScenes(ctx context.Context, identifier string, query url.Values) (*Page[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Page[SceneResource]
	if err := c.get(ctx, "/performers/"+url.PathEscape(identifier)+"/scenes", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPerformerJAV Get Performer Jav.
func (c *Client) GetPerformerJAV(ctx context.Context, identifier string, query url.Values) (*Page[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Page[SceneResource]
	if err := c.get(ctx, "/performers/"+url.PathEscape(identifier)+"/jav", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPerformerMovies Get Performer Movies.
func (c *Client) GetPerformerMovies(ctx context.Context, identifier string, query url.Values) (*Page[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Page[SceneResource]
	if err := c.get(ctx, "/performers/"+url.PathEscape(identifier)+"/movies", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetScene Get Scene.
func (c *Client) GetScene(ctx context.Context, identifier string, query url.Values) (*Response[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Response[SceneResource]
	if err := c.get(ctx, "/scenes/"+url.PathEscape(identifier)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetJAV Get Jav.
func (c *Client) GetJAV(ctx context.Context, identifier string, query url.Values) (*Response[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Response[SceneResource]
	if err := c.get(ctx, "/jav/"+url.PathEscape(identifier)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetMovie Get Movie.
func (c *Client) GetMovie(ctx context.Context, identifier string, query url.Values) (*Response[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Response[SceneResource]
	if err := c.get(ctx, "/movies/"+url.PathEscape(identifier)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindSceneHash Find Scene By Hash.
func (c *Client) FindSceneHash(ctx context.Context, hash string, query url.Values) (*Response[SceneResource], error) {
	if hash == "" || hash == "." || hash == ".." {
		return nil, fmt.Errorf("theporndb: invalid hash")
	}
	var result Response[SceneResource]
	if err := c.get(ctx, "/scenes/hash/"+url.PathEscape(hash)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindJAVHash Find Jav By Hash.
func (c *Client) FindJAVHash(ctx context.Context, hash string, query url.Values) (*Response[SceneResource], error) {
	if hash == "" || hash == "." || hash == ".." {
		return nil, fmt.Errorf("theporndb: invalid hash")
	}
	var result Response[SceneResource]
	if err := c.get(ctx, "/jav/hash/"+url.PathEscape(hash)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindMovieHash Find Movie By Hash.
func (c *Client) FindMovieHash(ctx context.Context, hash string, query url.Values) (*Response[SceneResource], error) {
	if hash == "" || hash == "." || hash == ".." {
		return nil, fmt.Errorf("theporndb: invalid hash")
	}
	var result Response[SceneResource]
	if err := c.get(ctx, "/movies/hash/"+url.PathEscape(hash)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetScenesSimilar Get similar Scenes.
func (c *Client) GetScenesSimilar(ctx context.Context, identifier string, query url.Values) (*Page[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Page[SceneResource]
	if err := c.get(ctx, "/scenes/"+url.PathEscape(identifier)+"/similar", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetJAVSimilar Get similar Jav.
func (c *Client) GetJAVSimilar(ctx context.Context, identifier string, query url.Values) (*Page[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Page[SceneResource]
	if err := c.get(ctx, "/jav/"+url.PathEscape(identifier)+"/similar", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetMoviesSimilar Get similar Movies.
func (c *Client) GetMoviesSimilar(ctx context.Context, identifier string, query url.Values) (*Page[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Page[SceneResource]
	if err := c.get(ctx, "/movies/"+url.PathEscape(identifier)+"/similar", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetScenesSimilarByIDs Get similar Scenes by multiple ids.
func (c *Client) GetScenesSimilarByIDs(ctx context.Context, query url.Values) (*Page[SceneResource], error) {
	var result Page[SceneResource]
	if err := c.get(ctx, "/scenes/similar", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetJAVSimilarByIDs Get similar Jav by multiple ids.
func (c *Client) GetJAVSimilarByIDs(ctx context.Context, query url.Values) (*Page[SceneResource], error) {
	var result Page[SceneResource]
	if err := c.get(ctx, "/jav/similar", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetMoviesSimilarByIDs Get similar Movies by multiple ids.
func (c *Client) GetMoviesSimilarByIDs(ctx context.Context, query url.Values) (*Page[SceneResource], error) {
	var result Page[SceneResource]
	if err := c.get(ctx, "/movies/similar", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetSiteScenes Get Site Scenes.
func (c *Client) GetSiteScenes(ctx context.Context, identifier string, query url.Values) (*Page[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Page[SceneResource]
	if err := c.get(ctx, "/sites/"+url.PathEscape(identifier)+"/scenes", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetSiteJAV Get Site Jav.
func (c *Client) GetSiteJAV(ctx context.Context, identifier string, query url.Values) (*Page[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Page[SceneResource]
	if err := c.get(ctx, "/sites/"+url.PathEscape(identifier)+"/jav", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetSiteMovies Get Site Movies.
func (c *Client) GetSiteMovies(ctx context.Context, identifier string, query url.Values) (*Page[SceneResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Page[SceneResource]
	if err := c.get(ctx, "/sites/"+url.PathEscape(identifier)+"/movies", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Changes Returns changes for an entity.
func (c *Client) Changes(ctx context.Context, entityType string, query url.Values) (*Page[ChangesResource], error) {
	if entityType == "" || entityType == "." || entityType == ".." {
		return nil, fmt.Errorf("theporndb: invalid type")
	}
	var result Page[ChangesResource]
	if err := c.get(ctx, "/changes/"+url.PathEscape(entityType)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListDirectors List/Search Directors.
func (c *Client) ListDirectors(ctx context.Context, query url.Values) (*Page[DirectorResource], error) {
	var result Page[DirectorResource]
	if err := c.get(ctx, "/directors", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListFavorites List/Show Favorites.
func (c *Client) ListFavorites(ctx context.Context, query url.Values) (*Page[int], error) {
	var result Page[int]
	if err := c.get(ctx, "/favourites", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Leaderboard Get User Leaderboard.
func (c *Client) Leaderboard(ctx context.Context, query url.Values) (*struct {
	Idx       int    `json:"idx"`
	UserID    int    `json:"user_id"`
	UserName  string `json:"user_name"`
	HashCount int    `json:"hash_count"`
}, error) {
	var result struct {
		Idx       int    `json:"idx"`
		UserID    int    `json:"user_id"`
		UserName  string `json:"user_name"`
		HashCount int    `json:"hash_count"`
	}
	if err := c.get(ctx, "/user/leaderboard", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListPerformerSites List/Search PerformerSites.
func (c *Client) ListPerformerSites(ctx context.Context, query url.Values) (*Page[PerformerSiteResource], error) {
	var result Page[PerformerSiteResource]
	if err := c.get(ctx, "/performer-sites", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPerformerSite Get PerformerSite.
func (c *Client) GetPerformerSite(ctx context.Context, identifier string, query url.Values) (*Response[PerformerSiteResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Response[PerformerSiteResource]
	if err := c.get(ctx, "/performer-sites/"+url.PathEscape(identifier)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListPerformers List/Search Performers.
func (c *Client) ListPerformers(ctx context.Context, query url.Values) (*Page[PerformerResource], error) {
	var result Page[PerformerResource]
	if err := c.get(ctx, "/performers", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPerformer Get Performer.
func (c *Client) GetPerformer(ctx context.Context, identifier string, query url.Values) (*Response[PerformerResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Response[PerformerResource]
	if err := c.get(ctx, "/performers/"+url.PathEscape(identifier)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPerformerSimilar Get Similar Performers.
func (c *Client) GetPerformerSimilar(ctx context.Context, identifier string, query url.Values) (*Response[PerformerResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Response[PerformerResource]
	if err := c.get(ctx, "/performers/"+url.PathEscape(identifier)+"/similar", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPerformerSimilarByIDs Get Similar Performers by IDs.
func (c *Client) GetPerformerSimilarByIDs(ctx context.Context, query url.Values) (*Response[PerformerResource], error) {
	var result Response[PerformerResource]
	if err := c.get(ctx, "/performers/similar", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetRating Get Rating.
func (c *Client) GetRating(ctx context.Context, query url.Values) (*struct {
	Rating float64 `json:"rating"`
}, error) {
	var result struct {
		Rating float64 `json:"rating"`
	}
	if err := c.get(ctx, "/ratings", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListSites List/Search Sites.
func (c *Client) ListSites(ctx context.Context, query url.Values) (*Page[SiteResource], error) {
	var result Page[SiteResource]
	if err := c.get(ctx, "/sites", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetSite Show Site.
func (c *Client) GetSite(ctx context.Context, identifier string, query url.Values) (*Response[SiteResource], error) {
	if identifier == "" || identifier == "." || identifier == ".." {
		return nil, fmt.Errorf("theporndb: invalid identifier")
	}
	var result Response[SiteResource]
	if err := c.get(ctx, "/sites/"+url.PathEscape(identifier)+"", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListTags List/Search Tags.
func (c *Client) ListTags(ctx context.Context, query url.Values) (*Page[TagResource], error) {
	var result Page[TagResource]
	if err := c.get(ctx, "/tags", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CheckCollection Check is a media item in the user's collection.
func (c *Client) CheckCollection(ctx context.Context, query url.Values) (*struct {
	Value bool `json:"value"`
}, error) {
	var result struct {
		Value bool `json:"value"`
	}
	if err := c.get(ctx, "/user/collection", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Me Your User.
func (c *Client) Me(ctx context.Context, query url.Values) (*UserResource, error) {
	var result UserResource
	if err := c.get(ctx, "/auth/user", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
