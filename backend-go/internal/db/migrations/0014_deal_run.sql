-- The deal hunter's last run, so a restart keeps its status and schedules
-- the next run one interval after this one started. A single row: a run
-- sets started_at when it begins, finished_at and result when it ends.
CREATE TABLE deal_run (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    result jsonb
);
