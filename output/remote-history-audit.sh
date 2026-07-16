set -eu
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
mysqlq() { docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw -uroot "$db" -e "$1"; }
uid=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT id FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM' ORDER BY id LIMIT 1")
printf '== TASK JSON ACCOUNTING FIELDS ==\n'
mysqlq "SELECT status,COUNT(*) total,SUM(JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.billing_state')) IS NOT NULL) with_state,SUM(COALESCE(JSON_EXTRACT(private_data,'$.token_id'),0)>0) with_token,SUM(COALESCE(JSON_EXTRACT(private_data,'$.pre_consumed_quota'),0)>0) with_preconsume,SUM(COALESCE(JSON_EXTRACT(private_data,'$.actual_quota'),0)>0) with_actual FROM tasks WHERE user_id=$uid GROUP BY status;"
printf '\n== FAILURE TASK TOKEN/QUOTA ==\n'
mysqlq "SELECT COALESCE(JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.token_id')),'0') token_id,COUNT(*) tasks,COALESCE(SUM(quota),0) quota_sum FROM tasks WHERE user_id=$uid AND status='FAILURE' GROUP BY COALESCE(JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.token_id')),'0') ORDER BY token_id;"
printf '\n== SUCCESS TASK TOKEN/QUOTA ==\n'
mysqlq "SELECT COALESCE(JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.token_id')),'0') token_id,COUNT(*) tasks,COALESCE(SUM(quota),0) quota_sum FROM tasks WHERE user_id=$uid AND status='SUCCESS' GROUP BY COALESCE(JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.token_id')),'0') ORDER BY token_id;"
printf '\n== REFUND LOG TOKEN/CHANNEL ==\n'
mysqlq "SELECT token_id,channel_id,COUNT(*) logs,COALESCE(SUM(quota),0) quota_sum FROM logs WHERE user_id=$uid AND type=6 GROUP BY token_id,channel_id ORDER BY token_id,channel_id;"
printf '\n== FAILURE TASK CHANNEL ==\n'
mysqlq "SELECT channel_id,COUNT(*) tasks,COALESCE(SUM(quota),0) quota_sum FROM tasks WHERE user_id=$uid AND status='FAILURE' GROUP BY channel_id ORDER BY channel_id;"
printf '\n== LOG/TASK REFUND TOTAL CHECK ==\n'
mysqlq "SELECT (SELECT COUNT(*) FROM tasks WHERE user_id=$uid AND status='FAILURE') failure_tasks,(SELECT COALESCE(SUM(quota),0) FROM tasks WHERE user_id=$uid AND status='FAILURE') failure_quota,(SELECT COUNT(*) FROM logs WHERE user_id=$uid AND type=6) refund_logs,(SELECT COALESCE(SUM(quota),0) FROM logs WHERE user_id=$uid AND type=6) refund_quota;"
printf '\n== USER/TOKEN/LOG ARITHMETIC ==\n'
mysqlq "SELECT u.used_quota user_used,(SELECT COALESCE(SUM(used_quota),0) FROM tokens WHERE user_id=u.id) token_used_sum,(SELECT COALESCE(SUM(CASE WHEN type=2 THEN quota WHEN type=6 THEN -quota ELSE 0 END),0) FROM logs WHERE user_id=u.id) settled_log,(SELECT COALESCE(SUM(CASE WHEN type=2 THEN quota ELSE 0 END),0) FROM logs WHERE user_id=u.id) gross_consume,(SELECT COALESCE(SUM(CASE WHEN type=6 THEN quota ELSE 0 END),0) FROM logs WHERE user_id=u.id) refund_sum,u.quota wallet_remaining FROM users u WHERE u.id=$uid;"
printf '\n== TASK FIELD SAMPLES (NO SECRET FIELDS) ==\n'
mysqlq "SELECT id,status,quota,channel_id,JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.token_id')) token_id,JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.billing_state')) billing_state,JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.usage_accounting_mode')) usage_mode,JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.billing_source')) billing_source,FROM_UNIXTIME(created_at) created_at FROM tasks WHERE user_id=$uid ORDER BY id DESC LIMIT 20;"
printf '\n== REFUND LOG OTHER FIELDS ==\n'
mysqlq "SELECT id,quota,token_id,channel_id,JSON_UNQUOTE(JSON_EXTRACT(other,'$.task_id')) task_id,JSON_UNQUOTE(JSON_EXTRACT(other,'$.reason')) reason,FROM_UNIXTIME(created_at) created_at FROM logs WHERE user_id=$uid AND type=6 ORDER BY id DESC LIMIT 20;"
