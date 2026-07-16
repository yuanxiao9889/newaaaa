set -eu
backup=/root/newapi-deploy-backups/billing-history-20260714-181326
ls -lh "$backup"
sha256sum -c "$backup/new-api.sql.gz.sha256"
gzip -t "$backup/new-api.sql.gz"
printf 'backup_ok\n'
