# v1 Usability Review Brief

Use this brief for VIGIL-AC-20. Recruit at least three participants outside the
implementation team across two profiles: a repository maintainer and an
automation or coding-agent integrator.

## Method

- Use an immutable release candidate and fresh user state.
- Do not coach participants beyond the public README and command help.
- Capture commands, completion state, errors, time, confidence, and unexpected
  mutations without collecting repository secrets.
- Ask participants to explain what Vigil will and will not protect before they
  authorize mutation.

## Required Scenarios

1. Run Vigil from an empty directory and identify available official packs.
2. Configure a fresh repository using dry-run before write.
3. Explain a read-only gate's process, network, and mutation contract.
4. Produce, review, and apply a plan.
5. Change config or repository state and recover from stale-plan rejection.
6. Diagnose a missing required tool and an optional missing tool.
7. Cancel a running workflow and confirm the child stops.
8. Trigger a read-only repository mutation and interpret the violation.
9. Preview, install, diagnose, and uninstall Git hooks without losing an
   existing hook.
10. Follow upgrade and rollback guidance without weakening plugin policy.
11. Locate private run evidence and create a redacted support-bundle preview.
12. For integrators, consume JSON/JSONL without parsing human prose.

## Blocking Outcomes

Treat these as P1 usability findings:

- a participant authorizes an unintended mutation because the preview is
  unclear;
- a participant believes read-only is an operating-system sandbox;
- a participant cannot distinguish optional tool skipping from successful
  execution;
- stale-plan, cancellation, mutation, or plugin-trust failures lead to unsafe
  recovery guidance;
- a supported install path cannot reach a meaningful preflight without source
  checkout knowledge.

## Deliverable

Report participant profiles, exact candidate, scenario completion, median and
range of completion time, errors, confidence, observed terminology conflicts,
P0/P1 findings, and recommended changes. Include redacted evidence for failures
and distinguish product defects from missing upstream tools.
