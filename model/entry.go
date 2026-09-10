package model

import "time"

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
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Type           string     `json:"type"`
	State          string     `json:"state"`
	LastSeenAt     *time.Time `json:"lastSeenAt,omitempty"`
	LastScanAt     *time.Time `json:"lastScanAt,omitempty"`
	ScanGeneration int64      `json:"scanGeneration"`
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
