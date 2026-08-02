-- The Milestone aggregate was renamed to Release. Activity entries persist the
-- event type as a string, so historical "MILESTONE_CLOSED" rows must be rewritten
-- to "RELEASE_CLOSED" to stay consistent with the renamed error codes and the
-- frontend activity icon map.
UPDATE activity_entries SET type = 'RELEASE_CLOSED' WHERE type = 'MILESTONE_CLOSED';
