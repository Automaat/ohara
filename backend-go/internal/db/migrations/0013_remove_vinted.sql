-- Vinted's terms forbid bots and data mining, so Ohara no longer reads it.
-- Its stored listings go too; their deal alerts follow through ON DELETE
-- CASCADE.
DELETE FROM price_observation WHERE source = 'vinted';
