# Security Policy

Please report suspected security issues privately. Vigil executes local
commands, evaluates repository mutation policy, and can run digest-bound
plugins, so security reports should assume command execution and supply-chain
impact are in scope when supported by a concrete path.

Email: info@paycaltech.com

## Supported Versions

| Version | Security support |
| --- | --- |
| `main` | Supported for coordinated reports against unreleased code. |
| `v0.2.0-beta.1` | Planned beta; supported after publication for release-workflow and beta-contract issues. |
| `v0.1.0` | Unsupported legacy bootstrap release; reports are triaged, but fixes target a newer release unless immediate user risk requires otherwise. |

No stable v1 release exists yet. Stable support windows are defined in
`docs/support-policy.md` after a supported stable line is published.

## Reporting

Include:

- affected version or commit;
- operating system and shell;
- reproduction steps;
- expected impact;
- any relevant logs or redacted configuration.

Do not include live secrets, private keys, access tokens, or unredacted
customer data. If a proof of concept requires sensitive material, describe the
shape of the data and wait for maintainer guidance.

## Response Targets

- Initial human acknowledgement: within 3 business days.
- Initial severity assessment: within 7 business days after enough information
  is available to reproduce or reason about the report.
- Status updates: at least every 14 days while a validated report remains open.

These are targets, not guarantees. Reports involving active exploitation,
credential exposure, or release integrity may be prioritized immediately.

## Severity

Maintainers assess severity from impact, exploitability, affected versions, and
whether the issue can cross repository, plugin, release, or credential
boundaries.

Examples likely to be treated as high severity include:

- executing an unapproved command or plugin despite policy denial;
- bypassing reviewed-plan digest checks;
- silently weakening signed plugin acquisition, trust, or revocation;
- publishing or accepting release assets with incorrect provenance;
- leaking secrets through logs, support bundles, artifacts, or machine output.

Examples usually treated below high severity include documentation ambiguity,
denial of service limited to a local command invocation, and behavior that
requires intentionally running an already untrusted command outside Vigil's
documented threat model.

## Coordinated Disclosure

Please give maintainers a reasonable opportunity to investigate and release a
fix before public disclosure. The default disclosure target is 90 days after
validation, or sooner by mutual agreement when a fix is available and users have
had time to upgrade. Maintainers may ask for an extension when the issue
requires external coordination, release infrastructure changes, or third-party
review.

Public advisories, CVEs, and release notes will credit reporters who want
credit and will avoid publishing exploit details that would create unnecessary
user risk before a fix is available.

## Safe Harbour

Good-faith research is welcome when it:

- avoids privacy violations, data destruction, persistence, or service
  disruption;
- uses only accounts, repositories, systems, and data you own or are authorized
  to test;
- does not attempt extortion, lateral movement, or secret extraction beyond what
  is necessary to demonstrate impact;
- reports findings promptly and privately.

Good-faith reports following this policy will not be treated as hostile solely
because they reveal a vulnerability.

## GitHub Private Vulnerability Reporting

If GitHub private vulnerability reporting is enabled for this repository, you
may use it instead of email. If it is not available, use
`info@paycaltech.com`.

Do not open public issues for vulnerabilities until a maintainer has reviewed
the report.
