# External Integration Report

Use this report for VIGIL-AC-16. The integrator must be independent of Vigil's
implementation and must test a plugin or machine-output consumer they control,
not only the checked-in fixture.

## Identity

- Integrator and organization:
- Relationship to Vigil:
- Report date:
- Vigil version, commit, and binary digest:
- Host operating system and architecture:
- Integration repository and immutable commit URL:

## Integration

- Implementation language and runtime:
- Plugin commands and capabilities, if applicable:
- Consumed output formats and schema versions:
- Installation and trust path:
- Repository policy used:

## Required Scenarios

Record commands, exit codes, stable diagnostic codes, and redacted evidence for:

1. handshake and metadata discovery;
2. exact digest lock and local capability approval;
3. successful command execution through the common envelope;
4. timeout or cancellation;
5. malformed response or incompatible protocol rejection;
6. executable or metadata digest drift rejection;
7. command collision or denied capability rejection;
8. JSON envelope success and failure consumption;
9. empty arrays, unknown additive data fields, and deterministic event order;
10. install, update, remove, and rollback behavior used by the integration.

Mark non-applicable scenarios and explain why. Do not omit failed scenarios.

## Findings

For each finding include severity, reproducible steps, expected and actual
behavior, affected contract, and whether it blocks adoption.

## Conclusion

- Contract usable without source checkout: yes/no
- Plugin protocol interoperable without Go code: yes/no/not applicable
- Machine output sufficient without parsing prose: yes/no
- P0/P1 findings: none/list
- Recommended acceptance status:
- Residual risks and requested changes:
