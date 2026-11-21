-- When a person last changed a title or an edition: an edit or a cover
-- photo. updated_at moves with the automatic cover fill too, so it cannot
-- tell. DELETE /api/requests/{key} removes the rows a failed add made only
-- while nobody changed them.
ALTER TABLE title ADD COLUMN edited_at timestamptz;

ALTER TABLE edition ADD COLUMN edited_at timestamptz;
