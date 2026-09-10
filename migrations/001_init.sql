CREATE TABLE storages (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'unknown',
    last_seen_at DATETIME,
    last_scan_at DATETIME,
    scan_generation INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE libraries (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE library_sources (
    id INTEGER PRIMARY KEY,
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    storage_id TEXT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    UNIQUE(library_id, storage_id, path)
);

CREATE TABLE entries (
    id TEXT PRIMARY KEY,
    storage_id TEXT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    parent_id TEXT REFERENCES entries(id),
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    type TEXT NOT NULL,
    size INTEGER NOT NULL DEFAULT 0,
    mtime DATETIME,
    inode INTEGER,
    device INTEGER,
    mime TEXT,
    extension TEXT,
    available INTEGER NOT NULL DEFAULT 1,
    scan_generation INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(storage_id, path)
);

CREATE INDEX idx_entries_parent ON entries(storage_id, parent_id);
CREATE INDEX idx_entries_path ON entries(storage_id, path);
CREATE INDEX idx_entries_name ON entries(name);
CREATE INDEX idx_entries_generation ON entries(storage_id, scan_generation);
CREATE INDEX idx_entries_identity ON entries(storage_id, device, inode);
