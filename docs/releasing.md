# Releasing Vigil

Recommended public release artifacts:

- macOS arm64 binary
- macOS amd64 binary
- Linux amd64 binary
- Linux arm64 binary
- SHA256 checksum file

Example build commands:

```bash
GOOS=darwin GOARCH=arm64 go build -o dist/vigil-darwin-arm64 ./cmd/vigil
GOOS=darwin GOARCH=amd64 go build -o dist/vigil-darwin-amd64 ./cmd/vigil
GOOS=linux GOARCH=amd64 go build -o dist/vigil-linux-amd64 ./cmd/vigil
GOOS=linux GOARCH=arm64 go build -o dist/vigil-linux-arm64 ./cmd/vigil
shasum -a 256 dist/vigil-* > dist/SHA256SUMS
```

GitHub Releases should attach the binaries and checksum file for each tagged
release.

## Release Checklist

1. Ensure `main` is green.
2. Confirm the CLI reports the intended version:

   ```bash
   go run ./cmd/vigil version
   ```

3. Tag a semantic version:

   ```bash
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

4. Attach release artifacts and `SHA256SUMS`.
5. Verify the release binary:

   ```bash
   ./dist/vigil-darwin-arm64 version
   ./dist/vigil-darwin-arm64 manpage > /tmp/vigil.1
   ```

## Homebrew Readiness

Homebrew formulae need stable, versioned source archives or release artifacts
with SHA-256 checksums and a meaningful formula test. See
`docs/homebrew-packaging.md` for the candidate formula and submission notes.
