#!/bin/sh
# lighthouse-light entrypoint. Forwards all arguments to the binary so
# `docker run oidfed/lighthouse-light /config.yaml` works (the upstream
# lighthouse entrypoint drops extra args).
set -e
exec /lighthouse-light "$@"
