#!/bin/sh
# Azure Pipelines starts a container job as `<image> bash -c "sleep infinity"`.
# Without this the scanner takes that as a subcommand, exits, and the job's
# setup execs land in a stopped container.
case "$1" in
  bash|sh) exec "$@" ;;
esac

exec /usr/local/bin/scanner "$@"
