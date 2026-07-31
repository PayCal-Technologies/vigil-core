# Vigil Extensions

This directory is the public extension contract.

Deployments can add:

```text
extensions/<extension-id>/extension.json
```

The manifest is intentionally JSON so humans, CI systems, and AI agents can
parse and generate it without custom syntax.

Required fields:

- `schema_version`: currently `1`.
- `id`: lowercase stable extension id.
- `name`: human-readable extension name.
- `kind`: extension family, such as `custom`.
- `status`: lifecycle state, usually `local`.
- `private`: whether the extension is local to the deployment.
- `public_core`: whether implementation code is part of the public core.
- `description`: concise purpose statement.
- `source_root`: implementation location in the deployment checkout.
- `packages`: implementation packages or source paths.
- `commands`: commands provided by the extension.
