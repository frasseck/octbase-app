// Friendly aliases over the generated OpenAPI types (37b stage 7).
//
// `openapi.d.ts` is generated from octbase-api/api/openapi.yaml and is not
// edited by hand; reaching into it directly from JSDoc means writing
// `import('../types/openapi').components['schemas']['Task']` at every use site.
// This file is the one place that indirection lives, so app code annotates with
// `import('../types/api').Task` and the shape of the generated file stays an
// implementation detail.
//
// Add an alias here when a view starts needing it — this list is not meant to
// mirror every schema, only the ones the SPA actually names.
import type { components } from './openapi';

type Schemas = components['schemas'];

export type User = Schemas['User'];
export type Project = Schemas['Project'];
export type ProjectPriority = Schemas['ProjectPriority'];
export type Membership = Schemas['Membership'];
export type MemberWithUser = Schemas['MemberWithUser'];
export type AssignableUser = Schemas['AssignableUser'];
export type Task = Schemas['Task'];
export type TaskComment = Schemas['TaskComment'];
export type TaskAttachment = Schemas['TaskAttachment'];
export type TaskRelation = Schemas['TaskRelation'];
export type TaskLink = Schemas['TaskLink'];
export type Board = Schemas['Board'];
export type BoardColumn = Schemas['BoardColumn'];
export type Release = Schemas['Release'];
export type Sprint = Schemas['Sprint'];
export type Page = Schemas['Page'];
export type ActivityEntry = Schemas['ActivityEntry'];
export type Error = Schemas['Error'];

// Every field in the generated schemas is optional, because the spec marks few
// of them `required`. That is weaker than it looks in one direction and exactly
// right in the other: it will not catch "this field can be absent", but it does
// catch a field that has been RENAMED or REMOVED — which is the failure the
// architecture doc's condition 2 was written about, and the one a Go `json:` tag
// change actually produces.
