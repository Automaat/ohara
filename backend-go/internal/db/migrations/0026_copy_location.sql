-- Where an owned copy is stored: one free-text place ("Regał 2", "Pudło
-- w piwnicy"), with no table of places, so renaming one means editing its
-- copies. Only owned copies sit somewhere; a wish has none. Null on every
-- copy stored before it.
ALTER TABLE copy ADD COLUMN location text;
ALTER TABLE copy ADD CONSTRAINT copy_location_owned CHECK (
    status = 'owned' OR location IS NULL
);
