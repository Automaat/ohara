-- When a run last moved a stored listing to another title and that
-- title's edition. The valuation counts an unowned edition's prices only
-- from listings matched to it after its last change; a moved listing was
-- matched then, not when it was first seen. Null for a listing that never
-- moved: its first observation is when it was matched.
ALTER TABLE price_observation ADD COLUMN moved_at timestamptz;
