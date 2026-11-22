-- The read-only link that shows the wishlist to anyone who has it. At most
-- one link exists: the single row's key is always true. Creating a link
-- again replaces its token; deleting the row turns the link off.
CREATE TABLE wishlist_share (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    token text NOT NULL CHECK (token ~ '^[A-Za-z0-9_-]{43}$'),
    created_at timestamptz NOT NULL DEFAULT now()
);
