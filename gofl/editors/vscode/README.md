# gofl VS Code extension (TV7)

Not started. The extension calls the `gofl` binary and reads its JSON output.

- Develop against `internal/finding/testdata/example.json` until `gofl crash` works (week 2).
- Check `version` in the JSON against `finding.SchemaVersion`.
- Week 1: verify how VS Code shows SARIF 2.1.0 (`rank`, `codeFlows`); this decides
  whether the extension reads SARIF or the JSON format.
