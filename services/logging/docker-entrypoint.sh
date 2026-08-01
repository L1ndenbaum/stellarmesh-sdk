#!/bin/sh
set -eu

data_dir="${STELLARMESH_LOGGING_DATA_DIR:-/var/lib/stellarmesh-logging}"
spool_file="${STELLARMESH_LOGGING_SPOOL_FILE:-$data_dir/spool/events.jsonl}"
error_audit_file="${STELLARMESH_LOGGING_ERROR_AUDIT_FILE:-$data_dir/archive/error_audit.jsonl}"

mkdir -p "$data_dir" "$(dirname "$spool_file")" "$(dirname "$error_audit_file")"
chown -R appuser:appuser "$data_dir"

exec su-exec appuser "$@"
