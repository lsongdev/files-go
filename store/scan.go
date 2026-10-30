package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lsongdev/files-go/config"
)

// ErrOffline means a root is missing or looks unmounted. The index is left
// untouched so an unplugged disk never wipes its entries.
var ErrOffline = errors.New("root is offline")

// Classify assigns a file kind at scan time from its name and size.
type Classify func(name string, size int64) string

type RootStatus struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type,omitempty"`
	FileID     int64     `json:"fileId"`
	State      string    `json:"state"` // idle, scanning, offline, error
	Scanned    int64     `json:"scanned"`
	Error      string    `json:"error,omitempty"`
	WatchError string    `json:"watchError,omitempty"`
	LastScan   time.Time `json:"lastScan,omitzero"`
}

type Scanner struct {
	store    *Store
	roots    []config.Root
	exclude  []string
	classify Classify

	scanMu   sync.Mutex   // serialize reconciliation, including manual requests
	watchDir func(string) // installed by Run; protected by mu
	mu       sync.Mutex
	status   map[string]*RootStatus
}

func NewScanner(store *Store, roots []config.Root, exclude []string, classify Classify) *Scanner {
	s := &Scanner{store: store, roots: roots, exclude: exclude, classify: classify, status: map[string]*RootStatus{}}
	for _, root := range roots {
		s.status[root.ID] = &RootStatus{ID: root.ID, Name: root.Name, Type: root.Type, State: "idle"}
	}
	return s
}

func (s *Scanner) Root(id string) (config.Root, bool) {
	for _, root := range s.roots {
		if root.ID == id {
			return root, true
		}
	}
	return config.Root{}, false
}

// Abs maps an indexed file to its filesystem path.
func (s *Scanner) Abs(f File) (string, error) {
	root, ok := s.Root(f.Root)
	if !ok {
		return "", fmt.Errorf("root %q is not configured", f.Root)
	}
	return filepath.Join(root.Path, filepath.FromSlash(f.Path)), nil
}

func (s *Scanner) Status() []RootStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]RootStatus, 0, len(s.roots))
	for _, root := range s.roots {
		result = append(result, *s.status[root.ID])
	}
	return result
}

func (s *Scanner) update(id string, fn func(*RootStatus)) {
	s.mu.Lock()
	fn(s.status[id])
	s.mu.Unlock()
}

// Init ensures every root has its directory row so it can be browsed
// before the first scan completes.
func (s *Scanner) Init(ctx context.Context) error {
	configured := make(map[string]bool, len(s.roots))
	for _, root := range s.roots {
		configured[root.ID] = true
	}
	// Read root IDs from the covering (root,path) index. A NOT IN DELETE
	// otherwise walks every file row, including large metadata, on each startup.
	rows, err := s.store.db.QueryContext(ctx, `SELECT DISTINCT root FROM files`)
	if err != nil {
		return err
	}
	var removed []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !configured[id] {
			removed = append(removed, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range removed {
		if _, err := s.store.db.ExecContext(ctx, `DELETE FROM files WHERE root=?`, id); err != nil {
			return err
		}
	}
	for _, root := range s.roots {
		f, err := s.ensureRoot(ctx, root)
		if err != nil {
			return err
		}
		s.update(root.ID, func(st *RootStatus) { st.FileID = f.ID })
	}
	return nil
}

func (s *Scanner) ensureRoot(ctx context.Context, root config.Root) (File, error) {
	if _, err := s.store.db.ExecContext(ctx, `INSERT INTO files(root, path, name, dir) VALUES (?, '', ?, 1)
		ON CONFLICT(root, path) DO UPDATE SET name=excluded.name`, root.ID, root.Name); err != nil {
		return File{}, err
	}
	return s.store.ByPath(ctx, root.ID, "")
}

// ScanAll walks every root sequentially.
func (s *Scanner) ScanAll(ctx context.Context) {
	for _, root := range s.roots {
		if err := s.Scan(ctx, root.ID, false); err != nil && ctx.Err() == nil {
			log.Printf("scan %s: %v", root.ID, err)
		}
	}
}

// Scan reconciles a whole root with the filesystem. force accepts a root
// that became empty although it cannot be told apart from an unmounted disk.
func (s *Scanner) Scan(ctx context.Context, rootID string, force bool) (err error) {
	root, ok := s.Root(rootID)
	if !ok {
		return fmt.Errorf("unknown root %q", rootID)
	}
	busy := false
	s.update(rootID, func(st *RootStatus) {
		busy = st.State == "scanning"
		if !busy {
			st.State, st.Scanned, st.Error = "scanning", 0, ""
		}
	})
	if busy {
		return nil
	}
	defer func() {
		s.update(rootID, func(st *RootStatus) {
			st.State, st.Error = "idle", ""
			switch {
			case errors.Is(err, ErrOffline):
				st.State = "offline"
			case err != nil:
				st.State, st.Error = "error", err.Error()
			default:
				st.LastScan = time.Now()
			}
		})
	}()
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	top, err := s.ensureRoot(ctx, root)
	if err != nil {
		return err
	}
	queue := []File{top}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		children, err := s.sync(ctx, root, dir, force)
		if errors.Is(err, fs.ErrNotExist) && dir.Path != "" {
			continue // removed while scanning; the parent's next sync drops it
		}
		if err != nil {
			return err
		}
		s.update(rootID, func(st *RootStatus) { st.Scanned += int64(len(children)) })
		for i := len(children) - 1; i >= 0; i-- {
			if children[i].Dir {
				queue = append(queue, children[i])
			}
		}
	}
	return nil
}

// SyncDir explicitly reconciles one directory and discovers any new subtrees.
func (s *Scanner) SyncDir(ctx context.Context, dir File) error {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	root, ok := s.Root(dir.Root)
	if !ok {
		return fmt.Errorf("unknown root %q", dir.Root)
	}
	// Reload because another scan may have updated this directory already.
	fresh, err := s.store.Get(ctx, dir.ID)
	if err != nil {
		return err
	}
	return s.syncNew(ctx, root, fresh)
}

// syncNew scans this level and recursively discovers new or changed subdirectories.
func (s *Scanner) syncNew(ctx context.Context, root config.Root, dir File) error {
	old, err := s.store.query(ctx, `SELECT `+columns+` FROM files WHERE parent=? AND dir=1`, dir.ID)
	if err != nil {
		return err
	}
	known := make(map[string]File, len(old))
	for _, f := range old {
		known[f.Path] = f
	}
	children, err := s.sync(ctx, root, dir, false)
	if err != nil {
		return err
	}
	for _, child := range children {
		previous := known[child.Path]
		if child.Dir && (previous.ID != child.ID || previous.MTime != child.MTime) {
			if err := s.syncNew(ctx, root, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Scanner) excluded(name string) bool {
	for _, pattern := range s.exclude {
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

type known struct {
	id          int64
	dir         bool
	size, mtime int64
}

// sync lists one directory, applies inserts, updates and deletions, and
// returns the current children.
func (s *Scanner) sync(ctx context.Context, root config.Root, dir File, force bool) ([]File, error) {
	abs := filepath.Join(root.Path, filepath.FromSlash(dir.Path))
	self, err := os.Stat(abs)
	if err != nil || !self.IsDir() {
		if dir.Path == "" {
			return nil, ErrOffline
		}
		if err == nil {
			err = fs.ErrNotExist
		}
		return nil, err
	}
	s.mu.Lock()
	watch := s.watchDir
	s.mu.Unlock()
	if watch != nil {
		watch(abs)
	} // register before listing to cover concurrent changes
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var current []File
	for _, entry := range entries {
		if s.excluded(entry.Name()) || !(entry.Type().IsRegular() || entry.IsDir()) {
			continue // symlinks and special files are not indexed
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", entry.Name(), err)
		}
		f := File{Root: root.ID, Parent: dir.ID, Name: entry.Name(), Path: path.Join(dir.Path, entry.Name()), Dir: entry.IsDir(), MTime: info.ModTime().UnixMilli()}
		if f.Dir {
			f.Kind = "folder" // Size (child count) is filled in when the folder itself is synced
		} else {
			f.Size = info.Size()
			f.Kind = s.classify(f.Name, f.Size)
		}
		current = append(current, f)
	}

	rows, err := s.store.db.QueryContext(ctx, `SELECT id, name, dir, size, mtime FROM files WHERE parent=?`, dir.ID)
	if err != nil {
		return nil, err
	}
	existing := map[string]known{}
	for rows.Next() {
		var name string
		var k known
		if err := rows.Scan(&k.id, &name, &k.dir, &k.size, &k.mtime); err != nil {
			rows.Close()
			return nil, err
		}
		existing[name] = k
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if dir.Path == "" {
		// A root that turned empty is either a directory that was emptied
		// or the mount point of a disk that is gone. Only the latter shows up
		// on a different filesystem than last time; when unsure, keep the
		// index unless forced.
		key := "root-device:" + root.ID
		device := strconv.FormatUint(deviceOf(self), 10)
		known, _ := s.store.CacheGet(ctx, key, 1<<62)
		if len(current) == 0 && len(existing) > 0 && !force && (device == "0" || string(known) != device) {
			return nil, ErrOffline
		}
		if device != "0" && string(known) != device {
			if err := s.store.CachePut(ctx, key, []byte(device)); err != nil {
				return nil, err
			}
		}
	}

	var changed []File
	var removed []string
	seen := make(map[string]bool, len(current))
	for i, f := range current {
		seen[f.Name] = true
		k, ok := existing[f.Name]
		if ok && k.dir != f.Dir {
			removed = append(removed, f.Path) // replaced by a different type
			ok = false
		}
		if ok && f.Dir {
			current[i].Size, f.Size = k.size, k.size
		}
		if ok && k.size == f.Size && k.mtime == f.MTime {
			current[i].ID = k.id
			continue
		}
		changed = append(changed, f)
	}
	for name := range existing {
		if !seen[name] {
			removed = append(removed, path.Join(dir.Path, name))
		}
	}
	if len(changed) == 0 && len(removed) == 0 && dir.MTime == self.ModTime().UnixMilli() {
		return current, nil
	}

	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, p := range removed {
		// Deletes the entry and its whole subtree ('0' sorts right after '/').
		if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE root=? AND (path=? OR (path>? AND path<?))`, root.ID, p, p+"/", p+"0"); err != nil {
			return nil, err
		}
	}
	upsert, err := tx.PrepareContext(ctx, `INSERT INTO files(root, parent, path, name, dir, size, mtime, kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(root, path) DO UPDATE SET parent=excluded.parent, dir=excluded.dir, size=excluded.size,
			mtime=excluded.mtime, kind=excluded.kind, meta_ver=0, revision=files.revision+1
		RETURNING id`)
	if err != nil {
		return nil, err
	}
	defer upsert.Close()
	ids := map[string]int64{}
	for _, f := range changed {
		var id int64
		if err := upsert.QueryRowContext(ctx, root.ID, dir.ID, f.Path, f.Name, f.Dir, f.Size, f.MTime, f.Kind).Scan(&id); err != nil {
			return nil, err
		}
		ids[f.Name] = id
	}
	// The directory row is re-enriched when its contents change (posters, NFO).
	if _, err := tx.ExecContext(ctx, `UPDATE files SET mtime=?, size=?, meta_ver=0, revision=revision+1 WHERE id=?`, self.ModTime().UnixMilli(), len(current), dir.ID); err != nil {
		return nil, err
	}
	// Re-identify only media whose shared inputs changed. Ordinary additions
	// in a collection must not reset unrelated subtrees. TV membership is
	// evidence for the containing Show, including changes inside a season.
	sidecarChanged, membershipChanged := false, false
	for _, f := range changed {
		_, existed := existing[f.Name]
		if strings.EqualFold(path.Ext(f.Name), ".nfo") || f.Kind == "image" {
			sidecarChanged = true
		}
		if !existed && (f.Dir || f.Kind == "video") {
			membershipChanged = true
		}
	}
	for _, p := range removed {
		k := existing[path.Base(p)]
		kind := s.classify(path.Base(p), k.size)
		if strings.EqualFold(path.Ext(p), ".nfo") || kind == "image" {
			sidecarChanged = true
		}
		if k.dir || kind == "video" {
			membershipChanged = true
		}
	}
	hint, base := root.TypeOf(dir.Path)
	if hint == "tv" && dir.Path != base && (sidecarChanged || membershipChanged) {
		rel := dir.Path
		if base != "" {
			rel = strings.TrimPrefix(rel, base+"/")
		}
		show := path.Join(base, strings.SplitN(rel, "/", 2)[0])
		if _, err := tx.ExecContext(ctx, `UPDATE files SET meta_ver=0,revision=revision+1 WHERE root=? AND dir=0
			AND kind='video' AND path>? AND path<?`, root.ID, show+"/", show+"0"); err != nil {
			return nil, err
		}
	} else if sidecarChanged {
		if _, err := tx.ExecContext(ctx, `UPDATE files SET meta_ver=0,revision=revision+1 WHERE parent=? AND dir=0
			AND kind IN ('video','audio')`, dir.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for i := range current {
		if id, ok := ids[current[i].Name]; ok {
			current[i].ID = id
		}
	}
	return current, nil
}

func jsonArray(values []any) string {
	data, _ := json.Marshal(values)
	return string(data)
}
