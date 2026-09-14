# Infracost CI

Scanner is the Go CLI that powers the Infracost GitHub Actions. It embeds the Infracost CLI as a library to scan directories of infrastructure code, calculate cost diffs, and post comments on pull requests.

The tool requires `git` at runtime to derive commit SHAs, branch names, and commit metadata from the checkout directories. Scanning and diffing logic is imported directly via `github.com/infracost/cli/pkg/scanner` rather than shelling out to the Infracost CLI.

Extracted from `infracost/actions@46b6838ed6f7af5263cce838b9b82427b270f29d` by FIX-723.

## Commands

- `scanner diff` — Scan base and head branches, compute a cost diff, post a PR comment, and upload results to the Infracost dashboard. Powers [`infracost/actions/diff`](https://github.com/infracost/actions/tree/master/diff).
- `scanner scan` — Scan a single directory and upload baseline results to the Infracost dashboard. Powers [`infracost/actions/scan`](https://github.com/infracost/actions/tree/master/scan).
- `scanner status` — Update the pull request status in the Infracost dashboard (OPEN, MERGED, CLOSED).

## Development

```bash
make build            # Build the binary
make test             # Run all tests
make test-unit        # Run unit tests only (skips integration tests)
make test-integration # Run integration tests (requires INFRACOST_CLI_AUTHENTICATION_TOKEN)
make lint             # Run golangci-lint
make mocks            # Regenerate mockery mocks
```

## Releasing

Releases are not yet published from this repository. The actions still download
`scanner/v*` binaries released from
[`infracost/actions`](https://github.com/infracost/actions/releases); that copy of the
source is frozen and changes belong here.

Releasing from this repository is FIX-724, and pointing the actions at those releases is
FIX-726.
