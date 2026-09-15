# dist/ is produced by .github/workflows/release.yml, not by this build. See
# "Development" in README.md for the local path.

# glibc, not Alpine: the amd64 plugin binaries on releases.infracost.io are
# dynamically linked (INTERP /lib64/ld-linux-x86-64.so.2, NEEDED libc.so.6)
# while the arm64 ones are static, so on musl all eleven fail to exec.

# Plugins live in a stage of their own so the ~450 MB layer is cacheable the
# day plugin versions are pinned. Until then latest is re-resolved every build.
FROM debian:trixie-slim AS plugins

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

ENV INFRACOST_CLI_PLUGIN_CACHE_DIRECTORY=/opt/infracost/plugins

ARG TARGETARCH
COPY dist/${TARGETARCH}/infracost-scanner /usr/local/bin/infracost-scanner

# install.go writes the directory and binaries 0750 as root, which denies
# every docker run --user. list exits non-zero on a plugin that installs but
# will not answer GetPluginInfo, so the build fails instead of image-verify.
RUN infracost-scanner plugins install && \
    chmod -R a+rX /opt/infracost/plugins && \
    infracost-scanner plugins list

FROM debian:trixie-slim

# git only: the v2 parsers read HCL rather than driving terraform, and the
# scanner reads a checkout it never clones. go-getter's hg:: sources are not
# covered.
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates git && \
    rm -rf /var/lib/apt/lists/*

# Errors from git are swallowed by internal/git, so a refused workspace would
# silently empty every commit field rather than fail. Cost: git honours
# .git/config from any mount, and core.fsmonitor/core.pager are exec.
RUN git config --system --add safe.directory '*'

# Without AUTO_UPDATE=false the first run re-resolves latest and tries to write
# into a directory it may not own.
ENV INFRACOST_CLI_PLUGIN_CACHE_DIRECTORY=/opt/infracost/plugins \
    INFRACOST_CLI_PLUGIN_AUTO_UPDATE=false

COPY --from=plugins /opt/infracost/plugins /opt/infracost/plugins

ARG TARGETARCH
COPY dist/${TARGETARCH}/infracost-scanner /usr/local/bin/infracost-scanner

# Only docker run consults this; a GitHub Actions container: job replaces it
# with its own shell, so the entrypoint takes subcommands.
ENTRYPOINT ["/usr/local/bin/infracost-scanner"]
