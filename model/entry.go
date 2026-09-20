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

type ParsedMedia struct {
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

// Media is the resolved, file-centric display enhancement. FileID may refer
// to either a file or a directory; a missing row means no enhancement.
type Media struct {
	FileID      string          `json:"fileId"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Icon        string          `json:"icon"`
	Backdrop    string          `json:"backdrop"`
	Year        *int            `json:"year,omitempty"`
	Line1       string          `json:"line1"`
	Line2       string          `json:"line2"`
	Line3       string          `json:"line3"`
	Summary     string          `json:"summary,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	Sources     json.RawMessage `json:"-"`
	MatchLocked bool            `json:"matchLocked,omitempty"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}
