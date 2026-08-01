# GitHub Actions Helper Extension

This is the canonical public GitHub extension for running Vigil preflight checks
inside GitHub Actions.

```bash
vigil github:init-ci
vigil github:init-ci --write
```

The generated workflow installs Vigil, runs `vigil verify --json`, and then
runs the configured checks from `vigil.config.json`.
