-- Configurable lane limits, sprint linkage, and cross-board read-only columns.

-- Per-board lane limits (absolute allowed range 1..10 is enforced in the
-- application layer). is_sprint_board marks a board as a Scrum sprint board;
-- sprint_id links it to an existing timed sprint configuration.
ALTER TABLE boards ADD COLUMN IF NOT EXISTS min_columns INTEGER NOT NULL DEFAULT 1;
ALTER TABLE boards ADD COLUMN IF NOT EXISTS max_columns INTEGER NOT NULL DEFAULT 10;
ALTER TABLE boards ADD COLUMN IF NOT EXISTS is_sprint_board INTEGER NOT NULL DEFAULT 0;
ALTER TABLE boards ADD COLUMN IF NOT EXISTS sprint_id TEXT REFERENCES sprints(id) ON DELETE SET NULL;

-- A board can surface columns belonging to another board in the same project as
-- read-only columns. source_column_id points at the foreign board_columns row.
CREATE TABLE IF NOT EXISTS board_external_columns (
  id               TEXT PRIMARY KEY,
  board_id         TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  source_column_id TEXT NOT NULL REFERENCES board_columns(id) ON DELETE CASCADE,
  position         INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL,
  UNIQUE (board_id, source_column_id)
);

CREATE INDEX IF NOT EXISTS idx_board_external_columns_board ON board_external_columns(board_id);
CREATE INDEX IF NOT EXISTS idx_boards_sprint_id ON boards(sprint_id);
