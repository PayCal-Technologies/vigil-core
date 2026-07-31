# GitHub CI/CD Extension

This is the canonical public GitHub extension for running Vigil in CI/CD.

```bash
vigil github:init-ci
vigil github:init-ci --write
```

The generated workflow installs Vigil Core, runs `vigil verify --json`, and then
runs the gates from `vigil.config.json`.
