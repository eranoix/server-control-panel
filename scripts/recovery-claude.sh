#!/usr/bin/env bash
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/internal/recoveryclaude/assets/manage.sh" "$@"
