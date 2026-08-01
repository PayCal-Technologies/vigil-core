# Reference Plugin

`vigil-plugin-reference` is a language-neutral subprocess fixture for Vigil
plugin protocol `1`. It has no dependency on Vigil source or Go packages.

Run the handshake-only conformance checks:

```bash
vigil --config examples/vigil.config.json \
  plugins:conformance \
  --file examples/plugins/reference/vigil-plugin-reference
```

Exercise every declared command in a disposable repository:

```bash
vigil --config examples/vigil.config.json \
  --allow-mutation \
  plugins:conformance \
  --file examples/plugins/reference/vigil-plugin-reference \
  --execute \
  --json
```

The example config permits local candidates. A repository whose policy requires
signed acquisition intentionally blocks this fixture.
