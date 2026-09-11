CREATE TABLE media_items (
    id               TEXT PRIMARY KEY,
    type             TEXT NOT NULL,
    title            TEXT NOT NULL,
    sort_title       TEXT NOT NULL DEFAULT '',
    year             INTEGER,
    parent_id        TEXT REFERENCES media_items(id) ON DELETE CASCADE,
    index_number     INTEGER,
    external_id      TEXT,
    match_source     TEXT NOT NULL DEFAULT '',
    match_confidence REAL NOT NULL DEFAULT 0,
    match_locked     INTEGER NOT NULL DEFAULT 0,
    metadata         TEXT NOT NULL DEFAULT '{}',
    created_at       DATETIME NOT NULL,
    updated_at       DATETIME NOT NULL
);

CREATE UNIQUE INDEX idx_media_items_external
ON media_items(type, external_id)
WHERE external_id IS NOT NULL;

CREATE INDEX idx_media_items_type_sort
ON media_items(type, sort_title, title, id);

CREATE INDEX idx_media_items_parent
ON media_items(parent_id, index_number, id);

CREATE TABLE media_item_files (
    media_id TEXT NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    role     TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY(media_id, entry_id, role)
);

CREATE INDEX idx_media_item_files_entry
ON media_item_files(entry_id, role, media_id);
