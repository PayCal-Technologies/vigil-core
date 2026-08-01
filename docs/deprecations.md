# Deprecation Policy

Stable public contracts are not removed without a documented transition.

## Required Sequence

1. Publish an accepted RFC or narrowly scoped compatibility decision.
2. Mark the command or contract `deprecated` in registry metadata.
3. Document the replacement, migration, rollback, and last supported release.
4. Emit a stable machine warning code and a concise text warning when used.
5. Preserve aliases and machine fields for at least two minor releases and
   90 calendar days, whichever is longer.
6. Remove only in the announced release, with release notes and compatibility
   tests updated in the same change.

Security issues may shorten the window when continued availability creates
material risk. The advisory must explain the exception and provide the safest
available migration.

Experimental contracts may change faster but still require release-note notice.
Published schema fields are never silently retyped or reassigned; incompatible
schema changes receive a new version and migration documentation.

## Current Deprecations

None.
