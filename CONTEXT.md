# MaaEnd Domain Language

MaaEnd automates Endfield tasks by recognizing game state and performing verified actions. This glossary records project-specific concepts shared across task and recognition design.

## Floating Recovery Grid Recognition

**Balloon Placement Plan**:
An ordered sequence of one entry per individual balloon, where each entry pairs the balloon's count-recognition node with one distinct placement-site grid position. Entries are ordered row-major from the upper-left grid position to the lower-right position.
_Avoid_: Per-value assignment, placement-site list

**Placement-Plan Cache**:
The complete Balloon Placement Plan produced during initial-state recognition, together with a placement index initially set to its first entry. Later workflow steps own and maintain that index as placements complete.
_Avoid_: Mutable solver queue

**Placement-Plan Consumption**:
The swipe-configuration action consumes one Placement-Plan Cache entry after it successfully overrides the next swipe's balloon source and target. It advances the index before that swipe executes; a subsequent swipe failure terminates the task and does not restore the index.
_Avoid_: Swipe-confirmed consumption, rollback on swipe failure

**Initial-State Caches**:
The placement-site positions, balloon configurations, and Placement-Plan Cache recognized for one initial state. Initial-state recognition clears all of them before work begins and writes them only after a complete valid state and plan are available.

**Balloon Configuration Count**:
The number of individual balloons represented by a recognized balloon value and its count-recognition node. Each individual balloon needs its own placement-plan entry, so a count node may occur more than once in the plan.
_Avoid_: Number of balloon-value types

A recognized count of zero is an invalid initial state and must fail initial-state recognition rather than be treated as an empty configuration.

**Balloon Weight**:
The value used in placement-plan balance equations. Initial-state recognition maps OCR values `1`, `2`, `3`, and `4` to weights `1`, `2`, `3`, and `6` respectively before solving.
_Avoid_: Raw OCR value

**Equivalent Placement Plan**:
Any Balloon Placement Plan whose placements are distinct placement sites and whose value-weighted horizontal and vertical coordinate sums are both zero. The solver may return any equivalent plan; no particular plan is canonical.
_Avoid_: Preferred solution, optimal solution

**Placement-Plan Order**:
The row-major order of a Balloon Placement Plan: increasing grid `Y` from top to bottom, then increasing grid `X` from left to right within each row.
_Avoid_: Column-major order, recognition order

An initial state with fewer placement sites than individual balloons, or with no Equivalent Placement Plan, is an invalid recognition result. Initial-state recognition must fail without retaining a Placement-Plan Cache.

**Grid ROI**:
The fixed 720p image region in which the floating-recovery square grid is expected to appear.
_Avoid_: Grid range, crop area

**Raw Line Segment**:
A finite, unclassified line fragment detected from the preprocessed Grid ROI.
_Avoid_: Grid line, candidate line family

**Candidate Line Family**:
One of the two sets of infinite grid-line candidates grouped by the source grid's horizontal or vertical direction before spacing-based cleansing or missing-line completion. Lines in each family may converge under perspective projection.
_Avoid_: Raw lines, clean grid lines

**Grid Vanishing Point**:
The finite or ideal image-plane point toward which one Candidate Line Family converges under perspective projection. A parallel family has an ideal point at infinity.
_Avoid_: Fixed line angle, parallel direction

**Cleansed Line Family**:
A Candidate Line Family after removing lines inconsistent with its shared vanishing geometry and merging near-duplicate observations. Its size is determined by image evidence rather than a predefined grid shape or line count.
_Avoid_: Completed grid, fixed line set

**Grid Point Center**:
The image-plane center of one logical floating-recovery grid position, inferred from local cell-sized line support and the globally consistent perspective lattice.
_Avoid_: Line intersection, Hough peak
