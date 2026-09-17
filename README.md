# Infracost CI

Cloud cost estimates for infrastructure code, in your CI pipeline.

`infracost-scanner` is a single Go binary that scans directories of infrastructure
code, calculates the cost difference between two branches, posts a comment on the
pull request, and uploads the run to [Infracost Cloud](https://dashboard.infracost.io).
It embeds the Infracost CLI as a library rather than shelling out to it.

- **Cost diffs on every PR** — what this change does to the monthly bill.
- **Guardrails, budgets and FinOps policies** — evaluated against the head branch, with a non-zero exit when a blocking one trips.
- **Baseline scans** — keep the dashboard current for the default branch.
- **No install step in CI** — the container image ships `git` and every parser and provider plugin.

## How it works

```mermaid
flowchart LR
  base[base checkout] --> scanner
  head[head checkout] --> scanner["infracost-scanner diff"]
  scanner <--> cloud[("Infracost Cloud<br/>policies · guardrails · budgets")]
  scanner --> comment["PR comment"]
  scanner --> gate{"blocking<br/>violation?"}
  gate -->|yes| fail["exit 1"]
  gate -->|no| pass["exit 0"]
```

`diff` takes **two checkouts of the same repository** — the base branch and the head
branch — not one path. `scan` is the single-directory command.

Across the life of a pull request:

```mermaid
sequenceDiagram
    participant Repo as Default branch
    participant PR as Pull request
    participant CI
    participant Cloud as Infracost Cloud
    Repo->>CI: push
    CI->>Cloud: scanner scan --path .
    PR->>CI: opened / synchronised
    CI->>Cloud: scanner diff --base-path … --head-path …
    Cloud-->>CI: policies, guardrails, budgets
    CI->>PR: cost comment (created or updated)
    PR->>CI: merged / closed
    CI->>Cloud: scanner status --status MERGED
```

## Provider support

| Provider | `scan` | `diff` | PR comment | `status` |
| --- | :---: | :---: | :---: | :---: |
| GitHub | ✅ | ✅ | ✅ | ✅ |
| GitLab | ✅ | ✅ | ✅ | ✅ |
| Bitbucket | ✅ | — | — | ✅ |
| Azure Repos | ✅ | ✅ | ✅ | ✅ |

Bitbucket cannot post comments, and `diff` always posts, so Bitbucket is `scan` and
`status` only — results appear in the dashboard, not in the pull request.

Self-managed servers work: GitHub Enterprise Server, self-managed GitLab and Azure
DevOps Server are all derived from `INFRACOST_VCS_REPOSITORY_URL`.

## Getting started

You need an [Infracost API key](https://dashboard.infracost.io) in
`INFRACOST_CLI_AUTHENTICATION_TOKEN`.

### GitHub Actions

```yaml
name: Infracost
on: [pull_request]

jobs:
  infracost:
    runs-on: ubuntu-latest
    container: ghcr.io/infracost/ci:0.1
    permissions:
      contents: read
      pull-requests: write
    env:
      INFRACOST_CLI_AUTHENTICATION_TOKEN: ${{ secrets.INFRACOST_API_KEY }}
      GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
      INFRACOST_VCS_PROVIDER: github
      INFRACOST_VCS_REPOSITORY_URL: ${{ github.server_url }}/${{ github.repository }}
      INFRACOST_VCS_PULL_REQUEST_ID: ${{ github.event.pull_request.number }}
      INFRACOST_VCS_PULL_REQUEST_TITLE: ${{ github.event.pull_request.title }}
      INFRACOST_VCS_PULL_REQUEST_AUTHOR: ${{ github.event.pull_request.user.login }}
      INFRACOST_VCS_BRANCH: ${{ github.head_ref }}
      INFRACOST_VCS_BASE_BRANCH: ${{ github.base_ref }}
      INFRACOST_VCS_PIPELINE_RUN_ID: ${{ github.run_id }}
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${{ github.event.pull_request.base.sha }}
          path: base
      - uses: actions/checkout@v4
        with:
          path: head
      - run: infracost-scanner diff --base-path base --head-path head
```

On GitHub Enterprise Server nothing extra is needed: the API URL is derived from
`INFRACOST_VCS_REPOSITORY_URL`. Override it with `--github-api-url` if it differs.
That flag wants the **base** server URL (the shape of `GITHUB_SERVER_URL`), not
`GITHUB_API_URL` — the latter ends `/api/v3`, and `/api/graphql` is appended to
whatever it is given. It is a flag only: `GITHUB_SERVER_URL` is `https://github.com`
on github.com, which is not an API URL.

The image entrypoint is the scanner, but a `container:` job replaces it with its own
shell — so the step names the binary rather than passing a subcommand to the image.

### GitLab CI

```yaml
infracost:
  image: ghcr.io/infracost/ci:0.1
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
  variables:
    # Full history: the shallow default may not contain the target branch.
    GIT_DEPTH: 0
    INFRACOST_VCS_PROVIDER: gitlab
    INFRACOST_VCS_REPOSITORY_URL: $CI_PROJECT_URL
    INFRACOST_VCS_PULL_REQUEST_ID: $CI_MERGE_REQUEST_IID
    INFRACOST_VCS_BRANCH: $CI_MERGE_REQUEST_SOURCE_BRANCH_NAME
    INFRACOST_VCS_BASE_BRANCH: $CI_MERGE_REQUEST_TARGET_BRANCH_NAME
    INFRACOST_VCS_PIPELINE_RUN_ID: $CI_PIPELINE_ID
  script:
    - git fetch origin "$CI_MERGE_REQUEST_TARGET_BRANCH_NAME"
    - git worktree add base "origin/$CI_MERGE_REQUEST_TARGET_BRANCH_NAME"
    - git worktree add head HEAD
    - infracost-scanner diff --base-path base --head-path head
```

`INFRACOST_CLI_AUTHENTICATION_TOKEN` and `GITLAB_TOKEN` both go in masked CI/CD
variables. `GITLAB_TOKEN` needs `api` scope to post notes — `CI_JOB_TOKEN` cannot,
so it is deliberately not a fallback.

`INFRACOST_VCS_PULL_REQUEST_ID` must be the project-scoped `iid`, not the global
merge request id. Self-managed GitLab needs nothing extra: the server URL is
derived from `INFRACOST_VCS_REPOSITORY_URL`. A GitLab served from a relative root
(`https://host/gitlab/group/repo`) needs `--gitlab-server-url` and
`--gitlab-project`.

To scan the default branch instead, run `scan --path .` and drop the
`INFRACOST_VCS_PULL_REQUEST_ID` variable.

### Bitbucket Pipelines

```yaml
image: ghcr.io/infracost/ci:0.1

pipelines:
  branches:
    main:
      - step:
          name: Infracost
          script:
            - export INFRACOST_VCS_PROVIDER=bitbucket
            - export INFRACOST_VCS_REPOSITORY_URL="https://bitbucket.org/$BITBUCKET_REPO_FULL_NAME"
            - export INFRACOST_VCS_BRANCH=$BITBUCKET_BRANCH
            - export INFRACOST_VCS_PIPELINE_RUN_ID=$BITBUCKET_BUILD_NUMBER
            - infracost-scanner scan --path .
```

### Azure Pipelines

```yaml
jobs:
  - job: infracost
    # diff is a pull request run: a build of the default branch has no target
    # branch to fetch. Run scan --path . there instead.
    condition: eq(variables['Build.Reason'], 'PullRequest')
    container: ghcr.io/infracost/ci:0.1
    variables:
      # Build.SourceBranchName is "merge" on a pull request build.
      headBranch: $[ replace(variables['System.PullRequest.SourceBranch'], 'refs/heads/', '') ]
    steps:
      - checkout: self
        fetchDepth: 0
        # The default drops the auth header, so the fetch below cannot reach a
        # private repository.
        persistCredentials: true
      - script: |
          git fetch origin "$(System.PullRequest.TargetBranchName)"
          git worktree add base "origin/$(System.PullRequest.TargetBranchName)"
          git worktree add head HEAD
      - script: infracost-scanner diff --base-path base --head-path head
        env:
          INFRACOST_CLI_AUTHENTICATION_TOKEN: $(INFRACOST_API_KEY)
          INFRACOST_VCS_PROVIDER: azure_repos
          INFRACOST_VCS_REPOSITORY_URL: $(Build.Repository.Uri)
          INFRACOST_VCS_PULL_REQUEST_ID: $(System.PullRequest.PullRequestId)
          INFRACOST_VCS_BRANCH: $(headBranch)
          INFRACOST_VCS_BASE_BRANCH: $(System.PullRequest.TargetBranchName)
          INFRACOST_VCS_PIPELINE_RUN_ID: $(Build.BuildId)
          SYSTEM_ACCESSTOKEN: $(System.AccessToken)
```

`SYSTEM_ACCESSTOKEN` must be mapped explicitly — Azure Pipelines does not expose
`System.AccessToken` to a step otherwise. Give the build service **Contribute to
pull requests** on the repository. A personal access token works too, via
`AZURE_DEVOPS_EXT_PAT`; only a 52-character PAT is sent as Basic auth, anything
else goes out as a bearer token.

`INFRACOST_VCS_REPOSITORY_URL` must be the repository URL containing `/_git/`, not
the project URL. `Build.Repository.Uri` already is one.

## Commands

| Command | What it does |
| --- | --- |
| `diff --base-path <dir> --head-path <dir>` | Scan both checkouts, compute the cost diff, post or update the PR comment, upload the run. Exits 1 on a new blocking guardrail or policy violation. Comments on GitHub, GitLab and Azure Repos; Bitbucket is `scan` only. |
| `scan --path <dir>` | Scan one directory and upload a branch run. |
| `status --status OPEN\|MERGED\|CLOSED` | Update the pull request state in the dashboard. |
| `plugins install\|list\|detect` | Install the parser and provider plugins, report their versions, or print the projects they identify. No auth token needed. |

`--tag` sets the marker `diff` uses to find its own comment (default
`infracost-comment`). Changing it on a repository that already has an Infracost
comment means the next run cannot find the old one and posts a second.

Run `infracost-scanner <command> --help` for the full flag list. Most VCS metadata
can come from a flag or an `INFRACOST_VCS_*` variable; the flag wins when both are
set. Paths are flags only.

## Configuration

| Variable | Notes |
| --- | --- |
| `INFRACOST_CLI_AUTHENTICATION_TOKEN` | Required by `diff` and `scan`. |
| `INFRACOST_VCS_PROVIDER` | `github`, `gitlab`, `azure_repos` or `bitbucket`. |
| `INFRACOST_VCS_REPOSITORY_URL` | Repository **web** URL. Required — never a clone URL with credentials in it. |
| `INFRACOST_VCS_PULL_REQUEST_ID` | PR number. On GitLab this is the project-scoped `iid`. |
| `INFRACOST_VCS_PULL_REQUEST_URL` | Alternative to the ID. Set both and they must agree. |
| `INFRACOST_VCS_BRANCH`, `INFRACOST_VCS_BASE_BRANCH` | Fall back to the checkout's git metadata. |
| `INFRACOST_VCS_PIPELINE_RUN_ID` | Links the run back to the CI job. |
| `INFRACOST_CI_DISABLE_DASHBOARD` | Skip uploading results. |
| `GITHUB_TOKEN`, `GITLAB_TOKEN`, `AZURE_DEVOPS_EXT_PAT` or `SYSTEM_ACCESSTOKEN` | Comment token for the provider `diff` is running against. |
| `INFRACOST_CI_VCS_TLS_CA_CERT_FILE` | PEM bundle trusted in addition to the system pool, for a self-managed VCS behind a private CA. VCS only — the dashboard, pricing and events clients use the system pool. |
| `INFRACOST_CI_VCS_TLS_INSECURE_SKIP_VERIFY` | Skip VCS certificate verification. The token then travels over an unauthenticated connection — prefer the CA file. Warns when on. |

Commit SHA, message, author and timestamp are read from the head checkout when not
set explicitly — which is why `git` must be on `PATH`. The container image has it.

## Installing

### Container (recommended)

The image carries `git` and every plugin, so a job pulls once instead of downloading
~150 MB of plugins on every run.

```bash
docker run --rm -e INFRACOST_CLI_AUTHENTICATION_TOKEN -v "$PWD:/src" -w /src \
  ghcr.io/infracost/ci:latest scan --path .
```

Tags are `latest`, the minor series (`0.1`) and the exact version (`0.1.0`). Only the
exact version is immutable; pin by digest to pin the bytes:

```bash
docker run --rm ghcr.io/infracost/ci@sha256:... --version
```

Each release publishes the manifest-list digest as `image-digest.txt`, and
`docker inspect` reports the plugin versions baked in under the `io.infracost.plugins`
label.

The image defaults to root, which matches what a GitHub Actions `container:` job does
and sidesteps uid mismatches against a mounted workspace. Under a `runAsNonRoot`
policy, give the user a writable home — the scanner caches there:

```bash
docker run --rm --user 65532 -e HOME=/tmp -e INFRACOST_CLI_AUTHENTICATION_TOKEN \
  -v "$PWD:/src" -w /src ghcr.io/infracost/ci:latest scan --path .
```

`git::` Terraform module sources work; `hg::` sources do not, since Mercurial is not
in the image.

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
# PowerShell 5.1 may default below TLS 1.2, and the progress stream makes a
# multi-megabyte -OutFile download very slow.
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

Assets are `infracost-scanner_<os>_<arch>.tar.gz` (linux, darwin),
`infracost-scanner_windows_<arch>.zip` and `checksums.txt`. Set
`INFRACOST_SCANNER_BASE_URL` to serve the same layout from your own mirror.
`checksums.txt` comes from the same host as the archive, so verification proves the
download was not corrupted — not that the host is honest. Only point it at a host
you trust.

Binary installs need `git` on `PATH`, and the first run downloads the plugins.

## Development

```bash
make build            # Build the binary
make test             # Run all tests
make test-unit        # Unit tests only
make test-integration # Integration tests (needs INFRACOST_CLI_AUTHENTICATION_TOKEN)
make lint             # golangci-lint
make mocks            # Regenerate mockery mocks
```

The `Dockerfile` copies a released binary rather than compiling one, so it does not
build from a clean checkout. To build it locally, put a binary where the release
workflow would:

```bash
mkdir -p "dist/$(go env GOARCH)"
CGO_ENABLED=0 go build -o "dist/$(go env GOARCH)/infracost-scanner" .
docker build -t infracost-ci:dev .
```

### Cutting a release

Push the tag. Nothing else.

```bash
git tag v0.1.0
git push origin v0.1.0
```

Do not use `gh release create`. The workflow drafts the release itself, builds
into it, verifies, and publishes last. A release that already exists and is
published makes the workflow refuse, because drafting over it would 404 every
pinned download and hand `latest` back to the previous version.

If that happens, delete the release and the tag, then push the tag again:

```bash
gh release delete v0.1.0 --repo infracost/ci --yes
git push --delete origin v0.1.0
```

Pushing a `v*.*.*` tag builds six platforms, attaches `checksums.txt`, publishes the
release, and pushes `ghcr.io/infracost/ci` for `linux/amd64` and `linux/arm64`. The
image is only tagged once the archives and the image have both been verified, so a
rolled-back release leaves nothing behind.

## License

[Apache 2.0](LICENSE)
