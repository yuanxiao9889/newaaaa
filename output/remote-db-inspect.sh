set -eu
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
mysqlq() { docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "$1"; }
printf '== TABLES ==\n'
mysqlq "SHOW TABLES" | grep -E '^(users|tokens|logs|tasks|subscriptions|user_subscriptions|subscription.*|redemptions|topups)$' || true
printf '\n== RELEVANT COLUMNS ==\n'
mysqlq "SELECT table_name,column_name,column_type,is_nullable FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name IN ('users','tokens','logs','tasks','subscriptions','user_subscriptions','topups','redemptions') ORDER BY table_name,ordinal_position;"
printf '\n== TARGET USER ==\n'
mysqlq "SELECT id,username,email,quota,used_quota,request_count,status,role,created_time FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM';"
