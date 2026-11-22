-- Loans of owned copies: who has a copy since when, until when and when it
-- came back. A loan stays after its return; a copy has at most one open
-- loan. A new table only; no stored row changes.
CREATE TABLE loan (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    copy_id bigint NOT NULL REFERENCES copy (id) ON DELETE CASCADE,
    borrower text NOT NULL CHECK (borrower <> '' AND octet_length(borrower) <= 500),
    lent_on date NOT NULL,
    due_on date CHECK (due_on >= lent_on),
    returned_on date CHECK (returned_on >= lent_on),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX loan_copy_id_idx ON loan (copy_id);
CREATE UNIQUE INDEX loan_open_key ON loan (copy_id) WHERE returned_on IS NULL;
