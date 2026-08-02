ALTER TABLE tasks DROP COLUMN IF EXISTS seq_number;
DROP TABLE IF EXISTS project_task_counters;
ALTER TABLE repository_connections DROP COLUMN IF EXISTS auto_close_on_merge;
ALTER TABLE branch_references DROP COLUMN IF EXISTS pr_number;
ALTER TABLE branch_references DROP COLUMN IF EXISTS pr_url;
ALTER TABLE branch_references DROP COLUMN IF EXISTS pr_status;
