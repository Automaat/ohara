-- OAuth token pairs for providers that need a user token (Allegro catalog).
-- One row per provider; the refresh token rotates on every refresh.
CREATE TABLE oauth_token (
    provider text PRIMARY KEY,
    access_token text NOT NULL,
    refresh_token text NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
