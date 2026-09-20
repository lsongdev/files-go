CREATE TABLE storages (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'unknown',
    last_seen_at DATETIME,
    last_scan_at DATETIME,
    scan_generation INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    scan_started_at DATETIME,
    scan_updated_at DATETIME,
    scan_entries INTEGER NOT NULL DEFAULT 0,
    scan_files INTEGER NOT NULL DEFAULT 0,
    scan_directories INTEGER NOT NULL DEFAULT 0,
    scan_estimate INTEGER NOT NULL DEFAULT 0,
    scan_error TEXT,
    scan_scope TEXT NOT NULL DEFAULT ''
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
CREATE INDEX idx_entries_name ON entries(name);
CREATE INDEX idx_entries_generation ON entries(storage_id, scan_generation);
CREATE INDEX idx_entries_identity ON entries(storage_id, device, inode);

CREATE VIRTUAL TABLE entry_search USING fts5(
    entry_id UNINDEXED,
    name,
    title,
    metadata,
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TRIGGER entries_search_insert AFTER INSERT ON entries BEGIN
    INSERT INTO entry_search(entry_id, name, title, metadata)
    VALUES (new.id, new.name, '', '');
END;

CREATE TRIGGER entries_search_update AFTER UPDATE OF id, name ON entries BEGIN
    UPDATE entry_search
    SET entry_id = new.id, name = new.name
    WHERE entry_id = old.id;
END;

CREATE TRIGGER entries_search_delete AFTER DELETE ON entries BEGIN
    DELETE FROM entry_search WHERE entry_id = old.id;
END;

CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    key TEXT,
    payload TEXT NOT NULL DEFAULT '{}',
    state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'done', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    priority INTEGER NOT NULL DEFAULT 0,
    run_after DATETIME,
    lease_until DATETIME,
    created_at DATETIME NOT NULL,
    started_at DATETIME,
    finished_at DATETIME,
    error TEXT,
    UNIQUE(type, key)
);

CREATE INDEX idx_jobs_claim ON jobs(state, priority DESC, run_after, created_at)
WHERE state = 'pending';
CREATE INDEX idx_jobs_lease ON jobs(lease_until) WHERE state = 'running';
CREATE INDEX idx_jobs_type_state ON jobs(type, state);

CREATE TABLE scan_checkpoints (
    storage_id TEXT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL,
    path TEXT NOT NULL,
    listed INTEGER NOT NULL DEFAULT 0,
    complete INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY(storage_id, generation, path)
);

CREATE INDEX idx_scan_checkpoints_resume ON scan_checkpoints(storage_id, generation, complete, path);

CREATE TABLE medias (
    file_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    year INTEGER,
    line1 TEXT NOT NULL DEFAULT '',
    line2 TEXT NOT NULL DEFAULT '',
    line3 TEXT NOT NULL DEFAULT '',
    backdrop TEXT NOT NULL DEFAULT '',
    data TEXT NOT NULL DEFAULT '{}',
    sources TEXT NOT NULL DEFAULT '{}',
    match_locked INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL
);

CREATE INDEX idx_medias_kind_year ON medias(kind, year, file_id);
