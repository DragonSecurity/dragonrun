#!/usr/bin/env bash
#
# Validates the compose file and shell scripts that go:embed puts in the binary.
# A syntax error in them compiles perfectly and only surfaces on a developer's
# machine during `dragonrun init`, so it is caught here instead.
#
# This is a script rather than steps repeated in two workflows because the two
# copies drifted the first time a service arrived with a new required
# credential: ci.yml was fixed, release.yml was not, and the difference only
# showed up after the tag had been pushed -- which is the one moment the repo
# is least able to absorb a mistake.
set -euo pipefail

compose=internal/stack/assets/docker-compose.yml

# Every ${VAR:?...} the compose file marks required, given a dummy value.
# Read out of the file rather than listed here: a hard-coded list is a second
# place to forget, and forgetting is the bug this script exists to stop
# repeating. SUPERUSER and DRAGONRUN_HOME carry defaults in the compose file
# but still need real-ish values -- an empty home would interpolate into a
# volume path of "/pgweb/bookmarks".
required=$(grep -oE '\$\{[A-Z_][A-Z0-9_]*:\?' "$compose" | sed 's/^\${//; s/:?$//' | sort -u)
echo "compose requires: $(echo "$required" | tr '\n' ' ')"

env_args=(SUPERUSER=ci DRAGONRUN_HOME=/tmp/dragonrun-ci)
for v in $required; do
  env_args+=("$v=ci")
done

# --profile dnsmasq so the profiled service is validated too; it is skipped by
# default and would otherwise be the one block nothing ever parses.
env "${env_args[@]}" docker compose -f "$compose" --profile dnsmasq config -q

find internal/stack/assets -name '*.sh' -print0 |
  xargs -0 shellcheck --severity=warning "$0"
