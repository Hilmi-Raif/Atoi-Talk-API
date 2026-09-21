#!/bin/sh
set -e

mkdir -p /var/lib/clickhouse/user_scripts
cp /usr/local/bin/histogramQuantile /var/lib/clickhouse/user_scripts/histogramQuantile
chmod 755 /var/lib/clickhouse/user_scripts/histogramQuantile
chown -R clickhouse:clickhouse /var/lib/clickhouse/user_scripts 2>/dev/null || true

exec /entrypoint.sh "$@"
