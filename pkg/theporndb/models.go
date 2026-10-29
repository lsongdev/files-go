// Code generated from ThePornDB OpenAPI v3.24.769; DO NOT EDIT.
package theporndb

import "encoding/json"

type CategoriesResource struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ChangesResource struct {
	ID   int    `json:"id"`
	UUID string `json:"uuid"`
}

type DirectorResource struct {
	ID   int    `json:"id"`
	UUID string `json:"uuid"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type MediaResource struct {
	ID    int    `json:"id"`
	URL   string `json:"url"`
	Size  int    `json:"size"`
	Order int    `json:"order"`
}

type PerformerResource struct {
	ID             string  `json:"id"`
	InternalID     int     `json:"_id"`
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	FullName       string  `json:"full_name"`
	Disambiguation string  `json:"disambiguation"`
	Bio            *string `json:"bio"`
	Rating         float64 `json:"rating"`
	IsParent       bool    `json:"is_parent"`
	Extras         struct {
		Gender            *string         `json:"gender"`
		Birthday          *string         `json:"birthday"`
		BirthdayTimestamp *float64        `json:"birthday_timestamp"`
		BirthdayType      *string         `json:"birthday_type"`
		Deathday          *string         `json:"deathday"`
		DeathdayTimestamp *float64        `json:"deathday_timestamp"`
		Birthplace        *string         `json:"birthplace"`
		BirthplaceCode    *string         `json:"birthplace_code"`
		Astrology         *string         `json:"astrology"`
		Ethnicity         *string         `json:"ethnicity"`
		Nationality       *string         `json:"nationality"`
		HairColour        *string         `json:"hair_colour"`
		EyeColour         *string         `json:"eye_colour"`
		Weight            *string         `json:"weight"`
		Height            *string         `json:"height"`
		Measurements      *string         `json:"measurements"`
		Cupsize           *string         `json:"cupsize"`
		Tattoos           *string         `json:"tattoos"`
		Piercings         *string         `json:"piercings"`
		Waist             *string         `json:"waist"`
		Hips              *string         `json:"hips"`
		CareerStartYear   *int            `json:"career_start_year"`
		CareerEndYear     *int            `json:"career_end_year"`
		FakeBoobs         *bool           `json:"fake_boobs"`
		SameSexOnly       *bool           `json:"same_sex_only"`
		Links             json.RawMessage `json:"links"`
	} `json:"extras"`
	Aliases        []string                `json:"aliases"`
	Image          *string                 `json:"image"`
	Thumbnail      *string                 `json:"thumbnail"`
	Face           *string                 `json:"face"`
	Posters        []MediaResource         `json:"posters"`
	SitePerformers []PerformerSiteResource `json:"site_performers"`
}

type PerformerSiteResource struct {
	ID         string                    `json:"id"`
	InternalID int                       `json:"_id"`
	SiteID     int                       `json:"site_id"`
	Name       string                    `json:"name"`
	Bio        string                    `json:"bio"`
	IsParent   bool                      `json:"is_parent"`
	Extra      *PerformerExtraDataObject `json:"extra"`
	Image      *string                   `json:"image"`
	Thumbnail  *string                   `json:"thumbnail"`
	Face       *string                   `json:"face"`
	Scenes     []SceneResource           `json:"scenes"`
	Parent     *PerformerResource        `json:"parent"`
	Site       *SiteResource             `json:"site"`
}

type MarkerResource struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	StartTime int    `json:"start_time"`
	EndTime   *int   `json:"end_time"`
	CreatedAt string `json:"created_at"`
}

type SceneResource struct {
	ID          string   `json:"id"`
	InternalID  float64  `json:"_id"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Slug        string   `json:"slug"`
	ExternalID  string   `json:"external_id"`
	Description string   `json:"description"`
	Rating      float64  `json:"rating"`
	SiteID      float64  `json:"site_id"`
	Date        string   `json:"date"`
	URL         string   `json:"url"`
	Image       *string  `json:"image"`
	BackImage   *string  `json:"back_image"`
	PosterImage *string  `json:"poster_image"`
	Poster      *string  `json:"poster"`
	Trailer     *string  `json:"trailer"`
	Duration    *float64 `json:"duration"`
	Format      *string  `json:"format"`
	SKU         *string  `json:"sku"`
	Background  struct {
		Full   *string `json:"full"`
		Large  *string `json:"large"`
		Medium *string `json:"medium"`
		Small  *string `json:"small"`
	} `json:"background"`
	BackgroundBack struct {
		Full   *string `json:"full"`
		Large  *string `json:"large"`
		Medium *string `json:"medium"`
		Small  *string `json:"small"`
	} `json:"background_back"`
	Posters struct {
		Full   *string `json:"full"`
		Large  *string `json:"large"`
		Medium *string `json:"medium"`
		Small  *string `json:"small"`
	} `json:"posters"`
	Media       *MediaResource           `json:"media"`
	Created     string                   `json:"created"`
	LastUpdated string                   `json:"last_updated"`
	Performers  []PerformerResource      `json:"performers"`
	Site        *SiteResource            `json:"site"`
	Tags        []TagResource            `json:"tags"`
	Hashes      []SceneHashBasicResponse `json:"hashes"`
	Markers     []MarkerResource         `json:"markers"`
	Directors   []DirectorResource       `json:"directors"`
	Scenes      []SceneResource          `json:"scenes"`
	Movies      []SceneResource          `json:"movies"`
	Links       json.RawMessage          `json:"links"`
}

type SiteResource struct {
	UUID        string        `json:"uuid"`
	ID          float64       `json:"id"`
	ParentID    *float64      `json:"parent_id"`
	NetworkID   *float64      `json:"network_id"`
	Name        string        `json:"name"`
	ShortName   string        `json:"short_name"`
	URL         string        `json:"url"`
	Description *string       `json:"description"`
	Rating      float64       `json:"rating"`
	Logo        *string       `json:"logo"`
	Favicon     *string       `json:"favicon"`
	Poster      *string       `json:"poster"`
	Network     *SiteResource `json:"network"`
	Parent      *SiteResource `json:"parent"`
}

type TagResource struct {
	ID      int           `json:"id"`
	UUID    string        `json:"uuid"`
	Name    string        `json:"name"`
	Parents []TagResource `json:"parents"`
}

type UserResource struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type MediaResponse json.RawMessage

type SceneHashBasicResponse struct {
	CanDelete   *bool           `json:"can_delete"`
	CreatedAt   string          `json:"created_at"`
	Duration    int             `json:"duration"`
	Hash        string          `json:"hash"`
	SceneID     int             `json:"scene_id"`
	Submissions int             `json:"submissions"`
	Type        json.RawMessage `json:"type"`
	UpdatedAt   string          `json:"updated_at"`
	Users       []float64       `json:"users"`
}

type SceneCardResponse struct {
	Background        json.RawMessage   `json:"background"`
	Date              *string           `json:"date"`
	DefaultBackground *string           `json:"default_background"`
	Duration          *int              `json:"duration"`
	ID                *int              `json:"id"`
	IsCollected       *bool             `json:"is_collected"`
	IsHidden          *bool             `json:"is_hidden"`
	Link              *string           `json:"link"`
	Performers        []json.RawMessage `json:"performers"`
	Site              json.RawMessage   `json:"site"`
	Slug              *string           `json:"slug"`
	Title             *string           `json:"title"`
	Type              *SceneTypeEnum    `json:"type"`
}

type SceneResponse struct {
	Directors json.RawMessage `json:"directors"`
}

type Meta struct {
	LastPage    int              `json:"last_page"`
	From        int              `json:"from"`
	To          int              `json:"to"`
	Path        string           `json:"path"`
	Links       []PaginationLink `json:"links"`
	Total       int              `json:"total"`
	PerPage     int              `json:"per_page"`
	CurrentPage int              `json:"current_page"`
	Last        int              `json:"last"`
}

type SceneTypeEnum string
type PerformerExtraDataObject map[string]json.RawMessage

type PaginationLink struct {
	URL    *string `json:"url"`
	Label  string  `json:"label"`
	Page   *int    `json:"page"`
	Active bool    `json:"active"`
}
