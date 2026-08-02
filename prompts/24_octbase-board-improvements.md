**Task: Make Kanban boards configurable and linkable to sprint timing**

**Description**
Enable project boards to be configurable so users can rename lanes, add and remove lanes within configurable limits, optionally use boards as Scrum sprint boards, and show columns from one board as read-only columns on another board within the same project.

**Requirements**

1. **Configurable lanes**

   * Users can rename board lanes.
   * Users can add new lanes.
   * Users can remove existing lanes.
   * The minimum number of lanes is configurable per board or project.
   * The maximum number of lanes is configurable per board or project.
   * Absolute allowed range:

     * Minimum: `1`
     * Maximum: `10`
   * Validation must prevent configurations below `1` or above `10`.

2. **Sprint-enabled boards**

   * Boards can be configured as Scrum sprint boards.
   * Existing timed sprint configuration must be linked to boards.
   * A board can be associated with a sprint configuration.
   * When a board is sprint-enabled, it should respect the configured sprint timing.

3. **Cross-board read-only columns**

   * Users can display any column from another board in the same project.
   * The displayed external column is read-only.
   * Users cannot move, edit, or remove tasks from the source column through the consuming board.
   * The source board and source column must be clearly identifiable.
   * Only boards within the same project are selectable.

**Acceptance Criteria**

* A user can rename any lane on a board.
* A user can add lanes up to the configured maximum.
* A user can remove lanes down to the configured minimum.
* The system rejects lane limits below `1` or above `10`.
* A board can be linked to an existing timed sprint configuration.
* A sprint-enabled board shows or uses the relevant sprint timing.
* A user can add a read-only column from another board in the same project.
* Tasks shown in a read-only external column cannot be modified from the consuming board.
* External columns cannot be selected from boards outside the current project.
