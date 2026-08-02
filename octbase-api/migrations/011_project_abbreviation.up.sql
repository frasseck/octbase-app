ALTER TABLE projects ADD COLUMN IF NOT EXISTS abbreviation VARCHAR(10) NOT NULL DEFAULT '';

UPDATE projects SET abbreviation = UPPER(LEFT(name, 2)) WHERE abbreviation = '';
