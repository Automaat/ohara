-- Ceneo's terms forbid automated access, so Ohara no longer asks it. Its
-- stored listings go too; their deal alerts follow through ON DELETE
-- CASCADE.
DELETE FROM price_observation WHERE source = 'ceneo';
