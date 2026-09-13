package model

import (
	"encoding/json"
	"time"
)

type EntryType string

const (
	EntryFile      EntryType = "file"
	EntryDirectory EntryType = "directory"
	EntrySymlink   EntryType = "symlink"
)

type Entry struct {
	ID        string  `json:"id"`
	StorageID string  `json:"-"`
	ParentID  *string `json:"parentId,omitempty"`

	Name string    `json:"name"`
	Path string    `json:"-"`
	Type EntryType `json:"type"`

	Size      int64  `json:"size"`
	MIME      string `json:"mime,omitempty"`
	Extension string `json:"extension,omitempty"`
	Available bool   `json:"available"`

	ModifiedAt time.Time `json:"modifiedAt"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`

	Inode          uint64 `json:"-"`
	Device         uint64 `json:"-"`
	ScanGeneration int64  `json:"-"`
}

type Storage struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Type            string     `json:"type"`
	State           string     `json:"state"`
	LastSeenAt      *time.Time `json:"lastSeenAt,omitempty"`
	LastScanAt      *time.Time `json:"lastScanAt,omitempty"`
	ScanStartedAt   *time.Time `json:"scanStartedAt,omitempty"`
	ScanUpdatedAt   *time.Time `json:"scanUpdatedAt,omitempty"`
	ScanGeneration  int64      `json:"scanGeneration"`
	ScanEntries     int64      `json:"scanEntries"`
	ScanFiles       int64      `json:"scanFiles"`
	ScanDirectories int64      `json:"scanDirectories"`
	ScanEstimate    int64      `json:"scanEstimate"`
	ScanError       string     `json:"scanError,omitempty"`
}

type Library struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Sources []LibrarySource `json:"sources,omitempty"`
}

type LibrarySource struct {
	StorageID string `json:"storageId"`
	Path      string `json:"-"`
	EntryID   string `json:"entryId,omitempty"`
}

type MediaFile struct {
	EntryID    string          `json:"entryId"`
	Kind       string          `json:"kind"`
	DurationMS *int64          `json:"durationMs,omitempty"`
	Container  string          `json:"container,omitempty"`
	Width      *int            `json:"width,omitempty"`
	Height     *int            `json:"height,omitempty"`
	VideoCodec string          `json:"videoCodec,omitempty"`
	AudioCodec string          `json:"audioCodec,omitempty"`
	Bitrate    *int64          `json:"bitrate,omitempty"`
	TakenAt    *time.Time      `json:"takenAt,omitempty"`
	Camera     string          `json:"camera,omitempty"`
	Latitude   *float64        `json:"latitude,omitempty"`
	Longitude  *float64        `json:"longitude,omitempty"`
	Metadata   json.RawMessage `json:"metadata"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

type Artifact struct {
	ID             string    `json:"id"`
	EntryID        string    `json:"entryId,omitempty"`
	MediaID        string    `json:"mediaId,omitempty"`
	Type           string    `json:"type"`
	Variant        string    `json:"variant,omitempty"`
	Key            string    `json:"-"`
	MIME           string    `json:"mime,omitempty"`
	Size           int64     `json:"size"`
	CreatedAt      time.Time `json:"createdAt"`
	LastAccessedAt time.Time `json:"lastAccessedAt"`
}

type MediaItem struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Title           string          `json:"title"`
	SortTitle       string          `json:"sortTitle,omitempty"`
	Year            *int            `json:"year,omitempty"`
	ParentID        string          `json:"parentId,omitempty"`
	IndexNumber     *int            `json:"indexNumber,omitempty"`
	ExternalID      string          `json:"externalId,omitempty"`
	MatchSource     string          `json:"matchSource,omitempty"`
	MatchConfidence float64         `json:"matchConfidence,omitempty"`
	MatchLocked     bool            `json:"matchLocked"`
	Metadata        json.RawMessage `json:"metadata"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	PrimaryEntryID  string          `json:"primaryEntryId,omitempty"`
	Files           []MediaItemFile `json:"files,omitempty"`
}

type MediaItemFile struct {
	MediaID   string    `json:"mediaId"`
	EntryID   string    `json:"entryId"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

type MediaSummary struct {
	ID              string  `json:"id"`
	Type            string  `json:"type"`
	Title           string  `json:"title"`
	Year            *int    `json:"year,omitempty"`
	IndexNumber     *int    `json:"indexNumber,omitempty"`
	MatchSource     string  `json:"matchSource,omitempty"`
	MatchConfidence float64 `json:"matchConfidence,omitempty"`
	PrimaryEntryID  string  `json:"primaryEntryId,omitempty"`
}

type PlaybackState struct {
	UserID     string     `json:"-"`
	MediaID    string     `json:"mediaId"`
	PositionMS int64      `json:"positionMs"`
	Played     bool       `json:"played"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	Media      *MediaItem `json:"media,omitempty"`
}
