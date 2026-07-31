# Scribe Extension

Scribe keeps a managed README snapshot block current without taking ownership
of the rest of the file.

```bash
vigil readme:generate
vigil readme:check
vigil readme:generate --dry-run
```

The public extension uses local repository facts such as dependency manifests,
test directories, git root, and loaded Vigil commands.
