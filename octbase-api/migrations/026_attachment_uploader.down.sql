DROP INDEX IF EXISTS idx_task_attachments_uploaded_by;
ALTER TABLE task_attachments DROP COLUMN IF EXISTS uploaded_by;
