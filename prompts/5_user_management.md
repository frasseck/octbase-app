You are a senior full-stack engineer with strong experience in security, RBAC, multi-user systems, and clean backend architecture.

Context:
I am developing a task-management app with:
- Frontend: JavaScript
- Backend: Go
- Goal: implement a production-ready user management system with roles and project permissions.

Important:
Do not implement impersonation.
The Super Admin must manage accounts and permissions directly as Super Admin.
Every Super Admin action must be audited.

Roles:

1. Super Admin
- Can manage all accounts.
- Can create, update, disable, and delete Admins, Users, and Guests.
- Can change global roles.
- Can view and manage all projects.
- Can assign users to any project.
- Can view audit logs.
- Cannot impersonate other users.

2. Admin
- Can create projects.
- Automatically becomes PROJECT_ADMIN for projects they create.
- Can assign Users and Guests to projects they administer.
- Can manage project members for their own projects.
- Can create, edit, and delete tasks in projects they administer.

3. User
- Can work in projects assigned to them.
- Can view, create, edit, and complete tasks in assigned projects.
- Cannot manage project permissions.
- Cannot manage other users.

4. Guest
- Can only read assigned projects.
- Cannot create, edit, delete, or complete tasks.
- Cannot manage users or permissions.

Permission model:
Use both global roles and project-level roles.

Global roles:
- SUPER_ADMIN
- ADMIN
- USER
- GUEST

Project-level roles:
- PROJECT_ADMIN
- PROJECT_MEMBER
- PROJECT_VIEWER

Role mapping:
- SUPER_ADMIN:
  - Full system access.
  - Can manage all users, roles, projects, memberships, and audit logs.
- ADMIN:
  - Can create projects.
  - Can manage projects where they are PROJECT_ADMIN.
- USER:
  - Has PROJECT_MEMBER access to assigned projects.
- GUEST:
  - Has PROJECT_VIEWER access to assigned projects.

Backend requirements for Go:

1. Data model:
Create or extend models for:

users:
- id
- email
- password_hash
- display_name
- global_role
- status: active, invited, disabled
- created_at
- updated_at
- last_login_at

projects:
- id
- name
- description
- created_by_user_id
- created_at
- updated_at
- archived_at nullable

project_memberships:
- id
- project_id
- user_id
- project_role: PROJECT_ADMIN, PROJECT_MEMBER, PROJECT_VIEWER
- assigned_by_user_id
- created_at
- updated_at

tasks:
- id
- project_id
- title
- description
- status
- assigned_to_user_id nullable
- created_by_user_id
- created_at
- updated_at
- deleted_at nullable

audit_logs:
- id
- actor_user_id
- action
- target_type
- target_id
- metadata_json
- ip_address
- user_agent
- created_at

2. Authentication:
- Implement login, logout, and current-user endpoint.
- Passwords must be securely hashed using bcrypt or Argon2id.
- Login errors must not reveal whether email or password was wrong.
- Disabled users must not be able to log in.
- Sessions or JWTs must be securely validated.
- If cookies are used:
  - HttpOnly
  - Secure in production
  - SameSite=Lax or Strict
- Add rate limiting for login and sensitive endpoints.

3. Authorization:
Implement centralized authorization helpers:

- CanManageAccounts(actor)
- CanCreateAdmin(actor)
- CanUpdateUserRole(actor, targetUser)
- CanDisableUser(actor, targetUser)
- CanCreateProject(actor)
- CanViewProject(actor, projectID)
- CanEditProject(actor, projectID)
- CanDeleteProject(actor, projectID)
- CanManageProjectMembers(actor, projectID)
- CanCreateTask(actor, projectID)
- CanEditTask(actor, taskID)
- CanDeleteTask(actor, taskID)
- CanViewTask(actor, taskID)
- CanViewAuditLogs(actor)

Rules:
- Super Admin can manage all accounts and all projects.
- Super Admin can create, update, disable, and delete Admins, Users, and Guests.
- Super Admin can change roles.
- Super Admin actions must be audited.
- Admin can only manage projects where they are PROJECT_ADMIN.
- Admin cannot create, update, disable, or delete Super Admins.
- Admin cannot create other Admins unless explicitly allowed.
- User can only access assigned projects.
- Guest has read-only access.
- Project access must be based on project_memberships, except for Super Admin.
- Frontend checks are only for UX; backend authorization is mandatory.

4. API endpoints:

Auth:
- POST /api/auth/login
- POST /api/auth/logout
- GET /api/auth/me

Users:
- GET /api/users
- POST /api/users
- GET /api/users/:id
- PATCH /api/users/:id
- PATCH /api/users/:id/disable
- DELETE /api/users/:id optional

Projects:
- GET /api/projects
- POST /api/projects
- GET /api/projects/:id
- PATCH /api/projects/:id
- PATCH /api/projects/:id/archive
- DELETE /api/projects/:id optional

Project Members:
- GET /api/projects/:id/members
- POST /api/projects/:id/members
- PATCH /api/projects/:id/members/:userId
- DELETE /api/projects/:id/members/:userId

Tasks:
- GET /api/projects/:projectId/tasks
- POST /api/projects/:projectId/tasks
- GET /api/tasks/:id
- PATCH /api/tasks/:id
- DELETE /api/tasks/:id

Audit:
- GET /api/audit-logs
Only Super Admins can access audit logs.

5. API response security:
- Never return password_hash.
- Never expose secrets or internal debug details.
- Keep client-facing errors generic.
- Log detailed errors server-side only.
- Never log passwords, tokens, cookies, authorization headers, or secrets.

Frontend requirements for JavaScript:

1. Implement an auth/user context:
- currentUser
- globalRole
- projectMemberships
- permission helpers

2. Role-based UI:

Super Admin:
- Account management
- Admin/User/Guest management
- Global project overview
- Project membership management
- Audit log view

Admin:
- Create projects
- Manage own projects
- Manage project members for own projects
- Manage tasks

User:
- View assigned projects
- Work on assigned project tasks

Guest:
- View assigned projects
- Read-only project and task UI

3. Frontend permission helpers:
Create centralized helpers:

- canManageAccounts()
- canCreateProject()
- canManageProjectMembers(project)
- canEditTask(task)
- canViewAuditLogs()
- isReadOnlyProject(project)

Important:
These frontend helpers are only for UI/UX.
The backend remains the only source of truth.

Security requirements:

1. Broken access control protection:
- Test every API with foreign project IDs and task IDs.
- A user must not access another user’s project by guessing an ID.

2. Privilege escalation protection:
- A User must not be able to make themselves an Admin.
- An Admin must not be able to create a Super Admin.
- An Admin must not be able to manage Super Admins.
- A Guest must never perform write actions.

3. Audit logging:
Create audit logs for:
- Failed login
- Successful login
- User created
- User updated
- User disabled
- User role changed
- Project created
- Project updated
- Project archived/deleted
- Project member added
- Project role changed
- Project member removed
- Task deleted

4. Input validation:
- Validate all request bodies.
- Validate IDs.
- Validate roles using allowlists.
- Prevent mass assignment.
- Users must not be able to set fields they are not allowed to set.

Tests:
Write backend tests for:
- Super Admin can create Admin.
- Super Admin can disable Admin.
- Super Admin can change user roles.
- Admin cannot create Super Admin.
- Admin cannot manage Super Admin.
- Admin can create project.
- Admin automatically becomes PROJECT_ADMIN for created project.
- Admin can add User to own project.
- User can only see assigned projects.
- Guest has read-only access.
- Guest cannot create tasks.
- User A cannot see User B’s project.
- User A cannot edit task from Project B.
- Disabled User cannot log in.
- Manipulated role fields are rejected.
- Super Admin actions are audited.

Frontend tests:
- Super Admin sees account management.
- Admin sees project management.
- User does not see user management.
- Guest sees read-only UI.
- Unauthorized buttons are hidden.
- 403 responses are handled correctly.

Implementation strategy:
1. Analyze the existing codebase.
2. Add or update data models and migrations.
3. Implement backend authentication and authorization.
4. Secure all API endpoints.
5. Implement frontend role-based UI.
6. Add backend and frontend tests.
7. Add short security documentation.

Output:
After implementation, provide:

1. Summary:
- What was implemented?
- Which roles exist?
- How does project access work?
- How does account management work?

2. Changed files:
- Backend
- Frontend
- Tests
- Migrations

3. Security decisions:
- Why this permission model was chosen.
- Which actions are audited.
- Which risks remain open.

4. Test commands:
- Go tests
- Frontend tests
- Linting
- Build

5. Open TODOs:
- Only real open points.
- No generic recommendations.