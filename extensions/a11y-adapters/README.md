# Accessibility Adapter Extension

This extension exposes public accessibility command contracts around local
tools. Missing tools report clearly instead of being hidden.

```bash
vigil a11y:inventory
vigil a11y:pa11y https://example.test
vigil a11y:lighthouse https://example.test
vigil a11y:playwright
vigil a11y:smoke --json
```

Projects can also wire their own accessibility commands into
`vigil.config.json` gates.
