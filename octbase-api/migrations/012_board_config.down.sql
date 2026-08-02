DROP TABLE IF EXISTS board_external_columns;

ALTER TABLE boards DROP COLUMN IF EXISTS sprint_id;
ALTER TABLE boards DROP COLUMN IF EXISTS is_sprint_board;
ALTER TABLE boards DROP COLUMN IF EXISTS max_columns;
ALTER TABLE boards DROP COLUMN IF EXISTS min_columns;
