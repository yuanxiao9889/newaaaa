set -eu
app=newapi_isgp-new-api-1
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
docker inspect "$app" --format 'image={{.Config.Image}} health={{.State.Health.Status}} restarts={{.RestartCount}} uptime_started={{.State.StartedAt}}'
docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw -uroot "$db" -e "SELECT \`key\`,value FROM options WHERE \`key\` IN ('QuotaPerUnit','DisplayInCurrencyEnabled','DisplayCurrency');"
