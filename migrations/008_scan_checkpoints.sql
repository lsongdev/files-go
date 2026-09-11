CREATE TABLE scan_checkpoints (
    storage_id TEXT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL,
    path TEXT NOT NULL,
    listed INTEGER NOT NULL DEFAULT 0,
    complete INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY(storage_id, generation, path)
);

CREATE INDEX idx_scan_checkpoints_resume
ON scan_checkpoints(storage_id, generation, complete, path);
