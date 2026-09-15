# Infracost CI

Scanner is the Go CLI that powers the Infracost GitHub Actions. It embeds the Infracost CLI as a library to scan directories of infrastructure code, calculate cost diffs, and post comments on pull requests.

The tool requires `git` at runtime to derive commit SHAs, branch names, and commit metadata from the checkout directories. Scanning and diffing logic is imported directly via `github.com/infracost/cli/pkg/scanner` rather than shelling out to the Infracost CLI.

Extracted from `infracost/actions@46b6838ed6f7af5263cce838b9b82427b270f29d` by FIX-723.

## Commands

- `scanner diff` — Scan base and head branches, compute a cost diff, post a PR comment, and upload results to the Infracost dashboard. Powers [`infracost/actions/diff`](https://github.com/infracost/actions/tree/master/diff).
- `scanner scan` — Scan a single directory and upload baseline results to the Infracost dashboard. Powers [`infracost/actions/scan`](https://github.com/infracost/actions/tree/master/scan).
- `scanner status` — Update the pull request status in the Infracost dashboard (OPEN, MERGED, CLOSED).
- `scanner plugins` — Install the parser and provider plugins (`install`), report the versions they answer with (`list`), and print the projects they identify in a directory (`detect`). None of the three need an authentication token.

## Installing

Anonymous download needs `infracost/ci` to be public; until it is, these recipes 404.

### Container

The container is the route most CI users should take: it carries `git` and all the
parser and provider plugins, so a job pulls once instead of downloading ~150 MB of
plugins on every run.

```bash
# docker run: the entrypoint is the scanner, so the subcommand is the argument
docker run --rm -e INFRACOST_CLI_AUTHENTICATION_TOKEN -v "$PWD:/src" -w /src \
  ghcr.io/infracost/ci:latest diff --base-path base --head-path head
```

Both CI forms below replace the entrypoint with their own shell, so they name the
binary rather than passing a subcommand to the image:

```yaml
# GitHub Actions
container: ghcr.io/infracost/ci:0.1.0
steps:
  - run: infracost-scanner diff --base-path base --head-path head
```

```yaml
# GitLab CI
infracost:
  image: ghcr.io/infracost/ci:0.1.0
  script:
    - infracost-scanner diff --base-path base --head-path head
```

`diff` takes two checkouts of the same repository, not one path. `scan --path .`
is the single-directory command.

Tags are `latest`, the minor series (`0.1`), and the exact version (`0.1.0`). Only
the exact version is immutable; pin by digest to pin the bytes:

```bash
docker run --rm ghcr.io/infracost/ci@sha256:... --version
```

The digest is published as `image-digest.txt` on each release, and
`docker inspect` reports the plugin versions the image was built with under the
`io.infracost.plugins` label.

The image defaults to root, which matches what a GitHub Actions `container:` job
does anyway and sidesteps uid mismatches against a mounted workspace. The plugin
directory is world-readable and executable, so `docker run --user 65532` loads the
plugins — but that uid has no home directory in the image, and the scanner caches
under it. A `runAsNonRoot` policy needs a writable one:

```bash
docker run --rm --user 65532 -e HOME=/tmp -e INFRACOST_CLI_AUTHENTICATION_TOKEN \
  -v "$PWD:/src" -w /src ghcr.io/infracost/ci:latest scan --path .
```

`git::` Terraform module sources work; `hg::` sources do not, since Mercurial is
not in the image.

### Binary

Linux and macOS:

```bash
BASE="${INFRACOST_SCANNER_BASE_URL:-https://github.com/infracost/ci/releases}"
REF="latest/download"        # or "download/v0.1.0" to pin
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m); case "$ARCH" in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; esac
SHA=$(command -v sha256sum || echo "shasum -a 256")   # macOS has no sha256sum

# Chained: an unverified archive must never reach tar.
ARCHIVE="infracost-scanner_${OS}_${ARCH}.tar.gz"
curl -fsSL -O "${BASE}/${REF}/${ARCHIVE}" &&
  curl -fsSL -O "${BASE}/${REF}/checksums.txt" &&
  grep " ${ARCHIVE}$" checksums.txt | $SHA -c - &&
  tar -xzf "$ARCHIVE"
```

Windows (PowerShell):

```powershell
# Windows PowerShell 5.1 needs both: it may default below TLS 1.2, and the
# progress stream makes a multi-megabyte -OutFile download very slow.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$ProgressPreference = "SilentlyContinue"

$Base = if ($env:INFRACOST_SCANNER_BASE_URL) { $env:INFRACOST_SCANNER_BASE_URL } else { "https://github.com/infracost/ci/releases" }
$Ref = "latest/download"     # or "download/v0.1.0" to pin
# An emulated x64 host on ARM64 reports AMD64; ARCHITEW6432 holds the real one.
$Machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
$Arch = if ($Machine -eq "ARM64") { "arm64" } else { "amd64" }

$Archive = "infracost-scanner_windows_$Arch.zip"
Invoke-WebRequest -UseBasicParsing -Uri "$Base/$Ref/$Archive" -OutFile $Archive
Invoke-WebRequest -UseBasicParsing -Uri "$Base/$Ref/checksums.txt" -OutFile checksums.txt

$Expected = (Select-String -Path checksums.txt -Pattern " $Archive$").Line.Split(" ")[0]
$Actual = (Get-FileHash -Algorithm SHA256 $Archive).Hash.ToLower()
if ($Expected -ne $Actual) { throw "checksum mismatch for $Archive" }

Expand-Archive -Path $Archive -DestinationPath . -Force
```

Set `INFRACOST_SCANNER_BASE_URL` to serve the same layout from somewhere other than
GitHub releases. `checksums.txt` comes from the same host as the archive, so the
verification proves the download was not corrupted — not that the host is honest.
Only point `BASE` at a host you trust.

## Development

```bash
make build            # Build the binary
make test             # Run all tests
make test-unit        # Run unit tests only (skips integration tests)
make test-integration # Run integration tests (requires INFRACOST_CLI_AUTHENTICATION_TOKEN)
make lint             # Run golangci-lint
make mocks            # Regenerate mockery mocks
```

The `Dockerfile` copies a released binary rather than compiling one, so it does not
build from a clean checkout — `dist/` is produced by the release workflow. To build
it locally, put a binary where the workflow would:

```bash
mkdir -p "dist/$(go env GOARCH)"
CGO_ENABLED=0 go build -o "dist/$(go env GOARCH)/infracost-scanner" .
docker build -t infracost-ci:dev .
```

## Releasing

Pushing a `v*.*.*` tag builds six platforms, attaches `checksums.txt`, and publishes
the release. Once the repository is public the assets are downloadable anonymously —
no `gh` CLI, no GitHub token.

```
infracost-scanner_<os>_<arch>.tar.gz     # linux, darwin
infracost-scanner_windows_<arch>.zip     # contains infracost-scanner.exe
checksums.txt
```

Asset names carry no version. `latest/download` is a plain redirect to the newest
release, and it only resolves while the name is identical across versions.

The same run publishes `ghcr.io/infracost/ci` for `linux/amd64` and `linux/arm64`,
built from the archives above rather than from a second compile. The exact version
tag is pushed first; `latest` and the minor tag are applied only after both the
archives and the image have been verified, and only when this release claimed the
`latest` redirect.
`image-digest.txt` carries the manifest-list digest and is attached to the release.

The GHCR package inherits the repository's private visibility. Making it public is a
manual step, taken at the same moment as making `infracost/ci` public (FIX-726).

Pointing the actions at these releases is FIX-726.
