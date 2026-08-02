-- 010_project_owner.down.sql: Reverse the PROJECT_OWNER migration.

UPDATE memberships SET role = 'PROJECT_ADMIN' WHERE role = 'PROJECT_OWNER';
