CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    type         TEXT NOT NULL,
    key          TEXT,
    payload      TEXT NOT NULL DEFAULT '{}',
    state        TEXT NOT NULL CHECK (state IN ('pending', 'running', 'done', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    priority     INTEGER NOT NULL DEFAULT 0,
    run_after    DATETIME,
    lease_until  DATETIME,
    created_at   DATETIME NOT NULL,
    started_at   DATETIME,
    finished_at  DATETIME,
    error        TEXT,
    UNIQUE(type, key)
);

CREATE INDEX idx_jobs_claim
ON jobs(state, priority DESC, run_after, created_at)
WHERE state = 'pending';

CREATE INDEX idx_jobs_lease
ON jobs(lease_until)
WHERE state = 'running';

