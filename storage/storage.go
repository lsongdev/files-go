package storage

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/lsongdev/files-go/model"
)

var (
	ErrNotFound      = errors.New("storage path not found")
	ErrPathTraversal = errors.New("storage path escapes root")
	ErrOffline       = errors.New("storage offline")
)

type FileInfo struct {
	Name       string
	Path       string
	Type       model.EntryType
	Size       int64
	ModifiedAt time.Time
	Inode      uint64
	Device     uint64
}

type Storage interface {
	Stat(context.Context, string) (FileInfo, error)
	ReadDir(context.Context, string) ([]FileInfo, error)
	Open(context.Context, string) (io.ReadSeekCloser, error)
	Mkdir(context.Context, string) error
	Rename(context.Context, string, string) error
	Remove(context.Context, string) error
}

type Registry struct {
	items map[string]Storage
}

func NewRegistry() *Registry { return &Registry{items: make(map[string]Storage)} }

func (r *Registry) Add(id string, s Storage) error {
	if id == "" || s == nil {
		return errors.New("storage id and implementation are required")
	}
	if _, exists := r.items[id]; exists {
		return errors.New("duplicate storage id: " + id)
	}
	r.items[id] = s
	return nil
}

func (r *Registry) Get(id string) (Storage, bool) {
	s, ok := r.items[id]
	return s, ok
}
