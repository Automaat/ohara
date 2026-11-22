-- One row per owned copy and day: the copy's buy-back, used and new value
-- as a valuation run computed them, null for a bound without prices. A
-- later run on the same day replaces the day's rows. A copy keeps its rows
-- once it stops being owned, so a sold copy still shows what it gained;
-- deleting the copy deletes them.
CREATE TABLE copy_value_history (
    copy_id bigint NOT NULL REFERENCES copy (id) ON DELETE CASCADE,
    day date NOT NULL,
    floor_cents bigint CHECK (floor_cents >= 0),
    mid_cents bigint CHECK (mid_cents >= 0),
    ceiling_cents bigint CHECK (ceiling_cents >= 0),
    computed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (copy_id, day)
);

CREATE INDEX copy_value_history_day_idx ON copy_value_history (day);
