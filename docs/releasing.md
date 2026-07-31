# Releasing Vigil Core

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
