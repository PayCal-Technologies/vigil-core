# Dependency Adapter Extension

This extension adds public dependency checks that work against common manifests
and locally installed package-manager tools.

```bash
vigil deps:inventory
vigil deps:why react
vigil checks:dependency-security --json
vigil npm:audit
vigil composer:validate
vigil php:lint --json
vigil phpstan:analyse
vigil javascript:quality --json
```
