#!/usr/bin/env bash
# recovery-claude.sh: shortcut to the recovery Claude manager.
#
# The implementation lives in internal/recoveryclaude/assets/manage.sh, next to the
# Dockerfile it builds, so the BINARY can embed the whole set and materialize it
# under <DataDir>/recovery-claude/ when needed. A running process must not depend
# on the repository's working directory: the binary is the deploy unit. This file
# only keeps the documented command working from inside the repo.
#
# Usage: scripts/recovery-claude.sh {build|up|down|restart|status|doctor|shell}
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/internal/recoveryclaude/assets/manage.sh" "$@"
