# Reviewed Plans

A Vigil plan is a complete, versioned description of the configured gates and
the evidence that makes those gates safe to execute without reinterpretation.

## Create And Review

Print a plan in the common JSON envelope:

```bash
vigil plan --json
```

Write a private plan file:

```bash
vigil --allow-mutation plan --output .vigil/plans/reviewed.json
```

Writing evidence is a filesystem mutation, so `--output` requires explicit
authorization. An in-repository plan path must be Git-ignored. Plan files are
written atomically with mode `0600`; existing plans require `--force`.

## Apply

```bash
vigil --allow-mutation apply .vigil/plans/reviewed.json
```

`apply` validates the plan schema and `plan_id`, recomputes every input, and
executes the exact gate array stored in the file. It does not reload gates from
the current config after validation.

The apply is blocked with exit `3` when any of these values changed:

- Vigil executable digest;
- config path or content digest;
- repository root or `HEAD`;
- Git-visible workspace digest;
- active command-registry digest;
- active pack-registry digest.

There is intentionally no stale-plan override. Generate and review a new plan.

## Trust Boundary

Plans provide local review integrity, not operating-system isolation. A plan can
authorize subprocess, network, filesystem, credential, or external-service
effects declared by its command and gate contracts. Inspect those contracts
before applying the plan.

Plan files reject unknown fields, unsupported schemas, malformed durations,
invalid digests, symlinks, oversized input, and any action change that does not
match the canonical `plan_id`.
