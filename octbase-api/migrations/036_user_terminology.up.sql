-- Vocabulary preference: 'AGILE' (sprint, backlog, epic, story points) or
-- 'CLASSIC' (phase, task pool, work package, effort points). Presentation only —
-- no column, endpoint or field name changes with it.
ALTER TABLE user_preferences
    ADD COLUMN IF NOT EXISTS terminology TEXT NOT NULL DEFAULT 'AGILE';
