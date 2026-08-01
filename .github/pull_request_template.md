## Summary

## Verification

- [ ] `go test ./...`
- [ ] `go build -o bin/vigil ./cmd/vigil`
- [ ] `vigil config:validate`

## Mutation Boundary

- [ ] This change preserves explicit confirmation for writes.
- [ ] JSON output remains deterministic where applicable.
