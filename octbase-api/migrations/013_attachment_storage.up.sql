-- Uploaded-file storage for task attachments.
--
-- An attachment is now either an external link (external_url) OR an uploaded
-- file (storage_key), never both. storage_key is an opaque, server-generated
-- key into the attachments storage volume (OCTBASE_ATTACHMENTS_DIR); it is
-- never derived from the user-supplied filename. Existing link-style rows keep
-- working: storage_key stays NULL for them.
ALTER TABLE task_attachments ADD COLUMN IF NOT EXISTS storage_key TEXT;
