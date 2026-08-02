-- Threaded comments: a comment may reply to another comment on the same task.
-- parent_id is NULL for top-level comments. ON DELETE CASCADE removes an entire
-- reply subtree when its parent is deleted, so the DeleteComment path stays a
-- single-row delete while keeping the tree consistent.
ALTER TABLE task_comments ADD COLUMN IF NOT EXISTS parent_id TEXT REFERENCES task_comments(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_task_comments_parent_id ON task_comments(parent_id);
