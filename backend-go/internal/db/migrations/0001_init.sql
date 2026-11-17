-- Baseline. Trigram matching backs fuzzy title search in the catalog.
-- pg_trgm is a trusted extension, so the database owner can create it.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
