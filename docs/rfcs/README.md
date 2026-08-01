# Vigil RFC Process

An RFC is required for changes to product scope, safety invariants, command or
schema compatibility, plugin trust, mutation policy, distribution channels, or
supported-platform commitments.

## Process

1. Copy [`0000-template.md`](0000-template.md) to a numbered proposal.
2. Open a GitHub discussion or issue linking the branch and rendered proposal.
3. Leave the proposal open for at least 14 calendar days.
4. Record concrete alternatives, security implications, migration, rollback,
   performance evidence, and unresolved objections.
5. A maintainer marks it `accepted`, `rejected`, or `withdrawn` in the document
   and links the decision.
6. Merge acceptance before implementation changes a stable contract.

Urgent security fixes may land under embargo. The public postmortem must state
which normal RFC steps were deferred and complete them when disclosure is safe.

Acceptance means permission to implement, not proof that the implementation is
correct. Normal review, tests, release gates, and deprecation policy still
apply.

The first public RFC should review the v1 command/config/output/plugin freeze.
Opening that discussion is an operational release task and cannot be completed
only by adding this repository process.

The prepared first proposal is
[`0001-v1-contract-freeze.md`](0001-v1-contract-freeze.md). Its status remains
`proposed` until a public discussion URL, opening date, minimum review period,
and decision are recorded.
