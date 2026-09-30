# Contributing

Thanks for considering a contribution to RunPilot.

## Getting started

Requirements: Go (see the `go` directive in `go.mod`).

```sh
go build ./...          # build everything
go vet ./...            # vet
make lint               # project linters (literal / web / hosts hygiene)
go test -race ./...     # full test suite
```

All four must pass before you open a pull request.

## Pull requests

- Keep changes focused. One logical change per PR.
- Follow the existing code style and package layout.
- Add or update tests for behavior changes.
- Do not commit secrets, real hostnames, private IPs, or personal data.
  The repo linters (`tools/linthosts`, `tools/lintlits`) enforce parts of this
  automatically.
- Write commit messages that explain *why*, not just *what*.

## License

By contributing, you agree that your contributions are licensed under the MIT
License (see [LICENSE](LICENSE)). No signed CLA is required; GitHub records
your commit as your contribution.

## Where to file issues

Use the [issue templates](.github/ISSUE_TEMPLATE/) for bug reports and feature
requests. For security concerns, see [SECURITY.md](SECURITY.md) — do not open a
public issue.
