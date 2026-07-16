set -eu
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
mysqlq() { docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw -uroot "$db" -e "$1"; }
uid=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT id FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM' ORDER BY id LIMIT 1")
if [ -z "$uid" ]; then echo 'TARGET_USER_NOT_FOUND'; exit 2; fi
printf '== USER ==\n'
mysqlq "SELECT id,username,email,quota,used_quota,request_count,status,role,FROM_UNIXTIME(created_at) created_at,deleted_at FROM users WHERE id=$uid;"
printf '\n== TOKENS ==\n'
mysqlq "SELECT id,name,status,remain_quota,used_quota,unlimited_quota,FROM_UNIXTIME(created_time) created_time,FROM_UNIXTIME(accessed_time) accessed_time,deleted_at FROM tokens WHERE user_id=$uid ORDER BY id;"
printf '\n== LOG TOTALS BY TYPE ==\n'
mysqlq "SELECT type,COUNT(*) rows_count,COALESCE(SUM(quota),0) quota_sum,MIN(FROM_UNIXTIME(created_at)) first_time,MAX(FROM_UNIXTIME(created_at)) last_time FROM logs WHERE user_id=$uid GROUP BY type ORDER BY type;"
printf '\n== LOG TOTALS BY TOKEN ==\n'
mysqlq "SELECT token_id,MAX(token_name) token_name,SUM(type=2) consume_rows,COALESCE(SUM(CASE WHEN type=2 THEN quota ELSE 0 END),0) consume_quota,SUM(type=6) refund_rows,COALESCE(SUM(CASE WHEN type=6 THEN quota ELSE 0 END),0) refund_quota,SUM(type=8) rollback_rows,COALESCE(SUM(CASE WHEN type=8 THEN quota ELSE 0 END),0) rollback_quota,COALESCE(SUM(CASE WHEN type=2 THEN quota WHEN type=6 THEN -quota ELSE 0 END),0) settled_quota FROM logs WHERE user_id=$uid GROUP BY token_id ORDER BY token_id;"
printf '\n== TASK TOTALS ==\n'
mysqlq "SELECT status,COUNT(*) rows_count,COALESCE(SUM(quota),0) quota_sum,MIN(FROM_UNIXTIME(created_at)) first_time,MAX(FROM_UNIXTIME(created_at)) last_time FROM tasks WHERE user_id=$uid GROUP BY status ORDER BY status;"
printf '\n== SUBSCRIPTIONS ==\n'
mysqlq "SELECT id,plan_id,amount_total,amount_used,status,source,FROM_UNIXTIME(start_time) start_time,FROM_UNIXTIME(end_time) end_time,allow_wallet_overflow FROM user_subscriptions WHERE user_id=$uid ORDER BY id;"
printf '\n== REDEMPTIONS ==\n'
mysqlq "SELECT id,name,status,quota,FROM_UNIXTIME(created_time) created_time,FROM_UNIXTIME(redeemed_time) redeemed_time FROM redemptions WHERE used_user_id=$uid OR user_id=$uid ORDER BY id;"
