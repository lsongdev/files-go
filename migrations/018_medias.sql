-- One resolved enhancement per physical file or directory. Existing media
-- tables remain during the staged migration and are not modified here.
CREATE TABLE medias (
    file_id      TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    icon         TEXT NOT NULL DEFAULT '',
    backdrop     TEXT NOT NULL DEFAULT '',
    year         INTEGER,
    line1        TEXT NOT NULL DEFAULT '',
    line2        TEXT NOT NULL DEFAULT '',
    line3        TEXT NOT NULL DEFAULT '',
    data         TEXT NOT NULL DEFAULT '{}',
    sources      TEXT NOT NULL DEFAULT '{}',
    match_locked INTEGER NOT NULL DEFAULT 0,
    updated_at   DATETIME NOT NULL
);

CREATE INDEX idx_medias_kind_year ON medias(kind, year, file_id);
