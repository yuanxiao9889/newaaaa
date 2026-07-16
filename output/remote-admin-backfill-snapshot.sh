set -eu
backup=/root/newapi-deploy-backups/billing-history-20260714-181326
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw -uroot "$db" -e "SELECT COUNT(*) topup_count FROM top_ups; SELECT id,user_id,amount,quota_amount,money,trade_no,payment_method,payment_provider,create_time,complete_time,status FROM top_ups ORDER BY id; SELECT \`key\`,value FROM options WHERE \`key\`='_internal.admin_quota_topup_backfill_v1'; SELECT id,user_id,created_at,content,request_id FROM logs WHERE type=3 AND (content LIKE '???%??????%' OR content LIKE 'Increased user quota by%') ORDER BY created_at,request_id,id;" > "$backup/admin-topup-backfill-before.tsv"
printf 'snapshot=%s\n' "$backup/admin-topup-backfill-before.tsv"
