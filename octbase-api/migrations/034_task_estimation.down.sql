ALTER TABLE tasks DROP COLUMN IF EXISTS estimate_hours;
ALTER TABLE tasks DROP COLUMN IF EXISTS story_points;
ALTER TABLE projects DROP COLUMN IF EXISTS estimation_unit;
