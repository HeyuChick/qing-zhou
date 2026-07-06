#!/bin/bash
# Daily backup of the qingzhou SQLite database. Keeps 7 days.
# Install: cp to /opt/qingzhou/backup.sh, chmod +x, add the cron in deploy/README.md.
set -e
DIR=/opt/qingzhou/backups
mkdir -p "$DIR"
TS=$(date +%Y%m%d-%H%M%S)

# Online hot backup (safe while the app is running).
sqlite3 /opt/qingzhou/qingzhou.db ".backup '$DIR/qingzhou-$TS.db'"

# Retention: delete backups older than 7 days.
find "$DIR" -maxdepth 1 -name '*.db' -mtime +7 -delete 2>/dev/null || true
echo "$(date '+%F %T') backup ok -> $DIR (qingzhou-$TS.db)"
