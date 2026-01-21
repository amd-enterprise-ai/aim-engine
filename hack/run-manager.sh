#!/bin/bash
# Wrapper script for running the manager with log rotation
# Creates timestamped logs in .tmp/logs/, keeps the 10 most recent

mkdir -p .tmp/logs
ls -t .tmp/logs/air-*.log 2>/dev/null | tail -n +11 | xargs rm -f 2>/dev/null
exec ./.tmp/manager 2>&1 | tee ".tmp/logs/air-$(date +%Y%m%d-%H%M%S).log"
