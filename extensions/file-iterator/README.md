# File Iterator Example Extension

This is the canonical golden-master extension for Vigil Core.

It demonstrates how an extension contributes command metadata without needing a
domain-specific integration. A real implementation of `files:iterate` would:

- accept a root directory and glob;
- walk matching files in stable sorted order;
- print one JSON object per file;
- avoid mutating files by default.

Example command contract:

```bash
vigil files:iterate --root=. --glob='**/*.go' --jsonl
```

Example output:

```jsonl
{"path":"cmd/vigil/main.go","size_bytes":12345}
{"path":"extensions/file-iterator/extension.json","size_bytes":512}
```
