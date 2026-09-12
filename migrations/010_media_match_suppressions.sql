CREATE TABLE media_match_suppressions (
    entry_id   TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL
);
