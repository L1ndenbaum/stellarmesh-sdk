#!/bin/sh
set -eu

data_dir="${STELLARMESH_LOGGING_DATA_DIR:-/var/lib/stellarmesh-logging}"
spool_dir="${STELLARMESH_LOGGING_SPOOL_DIR:-$data_dir/spool}"

mkdir -p "$data_dir" "$spool_dir/regular" "$spool_dir/priority"
chmod 700 "$data_dir" "$spool_dir" "$spool_dir/regular" "$spool_dir/priority"
chown -R appuser:appuser "$data_dir"

exec su-exec appuser "$@"
