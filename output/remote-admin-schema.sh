set -eu
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw -uroot "$db" -e "DESCRIBE top_ups; DESCRIBE options;"
