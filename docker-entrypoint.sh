#!/bin/sh
# Azure Pipelines starts a container job as `<image> bash -c "sleep infinity"`,
# possibly by absolute path. Without this the binary takes that as a subcommand,
# exits, and the job's setup execs land in a stopped container.
case "${1##*/}" in
  bash|sh) exec "$@" ;;
  scanner|infracost-scanner) shift ;;
esac

exec /usr/local/bin/infracost-ci "$@"
