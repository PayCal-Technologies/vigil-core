# Packs

Packs are declarative command and policy metadata. They are not dynamically
loaded Go code and are not, by themselves, executable plugins.

## Layers

Vigil loads layers in increasing precedence:

```text
embedded official < user < repository
```

Core commands are registered separately and cannot be replaced by pack
metadata. User packs default to the platform configuration directory under
`vigil/packs`; `VIGIL_USER_PACK_ROOT` provides an explicit override.
Repository packs are resolved relative to the selected config.
Manifest `source_root` values are provenance-only but must still be safe
relative slash-separated paths, for example `extensions/security-adapters`;
absolute paths, schemes, traversal, backslashes, and whitespace are rejected.

Each override is reported. Duplicate IDs within a layer and duplicate command
ownership in the effective set fail validation.

## Policy

Configuration can:

- disable all packs;
- restrict allowed `kind` values;
- require private manifests;
- allow only selected IDs;
- disable selected IDs;
- choose a repository manifest root.

The repository root must stay inside the config directory or discovered
repository boundary. Traversal and resolved symlink escapes fail.

## Command Contracts

Every command listed by a valid pack has one unique contract. The current
manifest schema requires command, access, capabilities, implementation binding,
timeout, stability, required tools, network behavior, expected output formats,
usage, and description. Host API compatibility is required at manifest scope
and applies to every command in that manifest. Missing, duplicate,
contradictory, and unsupported values fail closed.

Official and third-party manifests use
[`vigil-pack-v1.schema.json`](../../schemas/vigil-pack-v1.schema.json). The
runtime additionally enforces cross-field invariants that JSON Schema cannot
express, including one contract per command, exact built-in bindings, mutation
capabilities for mutating access, and agreement between network behavior and
the network capability.

## Plugins Are Separate

A plugin is a separately installed executable, not a pack. Vigil locates it
through a digest-bound repository lock, verifies a local trust record, performs
a versioned handshake, and then registers its namespaced commands. Plugin
processes exchange strict JSON over standard input and output; Go's native
plugin ABI is not used.

See [Plugins](plugins.md) for lifecycle, protocol, trust, and security details.
