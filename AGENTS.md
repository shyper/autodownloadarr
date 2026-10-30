# Required project verification

Read `CONTRIBUTING.md` and `.github/workflows/ci.yml` before changing this project.

Unless the user explicitly requests otherwise, before creating a commit or
reporting a change complete:

- Run `make lint test` in full. This includes `golangci-lint run`, not only
  `go vet`, and `go test -race ./...`, not only selected packages.
- For frontend changes, also run `npm run build` in `web`.
- Run `git diff --check`.

`gofmt` does not replace the configured `goimports` check. `go vet`, frontend
lint, passing unit tests and a successful build do not replace `golangci-lint`.

If a required tool is missing, install it within the user's permitted project
boundary and run the check. When investigating a CI failure, use the linter
version identified by that CI run. Keep downloaded tools, caches and temporary
files inside the project. If a required check cannot run, report it explicitly
as unverified; do not silently omit it or claim that all checks passed.

Fix the reported issues. Do not weaken checks or replace them with narrower
checks to obtain a passing result.
