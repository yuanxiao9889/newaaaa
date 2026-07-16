set -eu
app=newapi_isgp-new-api-1
mysqlc=newapi_isgp-mysql-1
workdir=/www/dk_project/dk_app/newapi/newapi_isGp
printf '== DEPLOY DIR ==\n'
ls -la "$workdir"
printf '\n== COMPOSE REDACTED ==\n'
sed -E 's#(SESSION_SECRET|REDIS_CONN_STRING|SQL_DSN|LOG_SQL_DSN|MYSQL_ROOT_PASSWORD|MYSQL_PASSWORD)([=:][[:space:]]*|=).*#\1=<redacted>#' "$workdir/docker-compose.yml" | sed -n '1,220p'
printf '\n== DATA ==\n'
find "$workdir/data" -maxdepth 2 -type f -printf '%p\t%s bytes\n' 2>/dev/null | sort
printf '\n== MYSQL SETTINGS ==\n'
docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | awk -F= '/^(MYSQL_DATABASE|MYSQL_USER)=/{print}'
printf '\n== APP DSN SHAPE ==\n'
docker exec "$app" sh -c 'case "$SQL_DSN" in *"@tcp("*) echo mysql;; postgresql:*|postgres:*) echo postgres;; "") echo sqlite-default;; *) echo other;; esac; if [ -n "$LOG_SQL_DSN" ]; then echo separate-log-db; else echo shared-log-db; fi'
printf '\n== RECENT BUILD DIRS ==\n'
find /root -maxdepth 1 -type d -name 'new-api-build-*' -printf '%TY-%Tm-%Td %TH:%TM\t%p\n' | sort -r | head -20
