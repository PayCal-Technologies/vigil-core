# Exit Codes

| Code | Meaning | Representative states |
| ---: | --- | --- |
| 0 | Success | `ok`, `skipped` |
| 1 | Check failed | `failed` |
| 2 | Usage or configuration error | invalid flags, invalid config |
| 3 | Policy blocked execution | `blocked` |
| 4 | Required dependency missing | `tool_missing` |
| 5 | Timeout or cancellation | `timed_out`, `cancelled` |
| 6 | Mutation violation | `mutation_detected` |
| 7 | Internal Vigil failure | `internal_error` |

Commands that aggregate multiple checks return the most specific applicable
non-zero code. Structured check results carry their exit class through the
common JSON envelope and all derived machine formats. Unknown child or handler
codes normalize to `7` and cannot become new public meanings accidentally.
