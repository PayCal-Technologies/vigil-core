# Security Policy

Please report suspected security issues privately. Vigil executes local
commands, loads repository policy, and supports plugin/publisher workflows, so
command execution boundaries, supply-chain integrity, secret handling, and
trust prompts are in scope for coordinated disclosure.

Email: info@paycaltech.com

## Supported Versions

| Version | Security support |
| --- | --- |
| `main` | Supported for coordinated reports against unreleased code. |
| `v0.2.0-beta.1` | Planned beta. Supported after publication for release workflow, archive, signing, attestation, Homebrew-candidate, command-boundary, and beta-contract issues. |
| `v0.1.0` | Unsupported legacy bootstrap release. Reports are triaged, but fixes normally target `main` and the next beta/stable release unless there is immediate user risk in the published artifact. |

## Reporting

Include:

- affected version or commit;
- operating system and shell;
- reproduction steps;
- expected impact;
- any relevant logs or redacted configuration.

Do not include live secrets, private keys, access tokens, or third-party data in
reports. If a proof of concept needs sensitive material, describe the minimum
shape of the input and wait for maintainer guidance.

Do not open public issues for vulnerabilities until a maintainer has reviewed
the report.

When GitHub private vulnerability reporting is enabled for this repository, you
may also use the repository's **Security** tab to submit a private vulnerability
report. Email remains the current private contact.

## Response Targets

Maintainers aim to acknowledge new reports within 3 business days, provide an
initial severity assessment within 7 business days after enough information is
available, and send status updates at least every 14 days while a coordinated
fix is in progress.

Severity is assessed from practical impact, exploitability, affected versions,
and whether the issue can cause unintended command execution, policy bypass,
secret exposure, artifact substitution, trust-boundary confusion, or misleading
release evidence.

## Disclosure Timeline

The default coordinated-disclosure window is 90 days from maintainer
acknowledgement. Shorter timelines may be appropriate for actively exploited
issues or low-risk documentation fixes. Longer timelines may be appropriate
when users need migration time or when the fix depends on upstream platform
changes. Public disclosure should wait until a fix, mitigation, or explicit
maintainer status note is available.

## Safe Harbour

Good-faith research is welcome when it avoids privacy violations, data
destruction, persistence, lateral movement, denial of service, and access to
systems or accounts you do not own or have permission to test. Keep testing to
your own repositories, local machines, and disposable credentials. Stop and
report promptly if you encounter sensitive data or a boundary you did not
intend to cross.
