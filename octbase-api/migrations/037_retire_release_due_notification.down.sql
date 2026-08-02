-- Deliberately empty.
--
-- The up migration deletes preference rows for a notification kind that was
-- never emitted, so there is nothing whose absence changes behaviour and
-- nothing to reconstruct: which users had toggled a setting for a notification
-- they never received is not information worth inventing on a rollback.
-- Re-adding the kind to the code would let users set it again from scratch.
SELECT 1;
