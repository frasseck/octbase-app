-- Reverting drops the render parameters. Every row keeps its English `message`,
-- which was never stopped being written, so nothing is blanked — the bell just
-- goes back to reading English to everyone.
ALTER TABLE notifications DROP COLUMN IF EXISTS params_json;
