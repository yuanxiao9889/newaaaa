set -eu
backup=/root/newapi-deploy-backups/billing-history-20260714-181326
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
uid=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT id FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM' ORDER BY id LIMIT 1")
cutoff=142557
condition="l.user_id=$uid AND l.type=6 AND l.id<=$cutoff AND t.status='FAILURE' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(t.private_data,'$.billing_state')),'')='refunded' AND (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(t.private_data,'$.usage_accounting_mode')),'')='final' OR (JSON_EXTRACT(t.private_data,'$.usage_accounting_mode') IS NULL AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(t.private_data,'$.internal_async')),'false')='true'))"
pre=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT COUNT(*),COALESCE(SUM(l.quota),0) FROM logs l JOIN tasks t ON t.user_id=l.user_id AND t.task_id=JSON_UNQUOTE(JSON_EXTRACT(l.other,'$.task_id')) WHERE $condition;")
set -- $pre
[ "$1" = "172" ] && [ "$2" = "15350000" ] || { echo "unexpected eligible set: $pre"; exit 1; }
docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw -uroot "$db" -e "SELECT l.* FROM logs l JOIN tasks t ON t.user_id=l.user_id AND t.task_id=JSON_UNQUOTE(JSON_EXTRACT(l.other,'$.task_id')) WHERE $condition ORDER BY l.id;" > "$backup/AIGC-YCDM-reclassified-logs-before.tsv"
ids=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT l.id FROM logs l JOIN tasks t ON t.user_id=l.user_id AND t.task_id=JSON_UNQUOTE(JSON_EXTRACT(l.other,'$.task_id')) WHERE $condition ORDER BY l.id;" | paste -sd, -)
cat > "$backup/rollback-history-correction.sql" <<SQL
START TRANSACTION;
UPDATE logs SET type=6 WHERE user_id=$uid AND type=8 AND id IN ($ids);
COMMIT;
SQL
result=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "START TRANSACTION; UPDATE logs l JOIN tasks t ON t.user_id=l.user_id AND t.task_id=JSON_UNQUOTE(JSON_EXTRACT(l.other,'$.task_id')) SET l.type=8 WHERE $condition; SELECT ROW_COUNT(); COMMIT;")
[ "$result" = "172" ] || { echo "unexpected updated rows: $result"; exit 1; }
printf 'updated_rows=%s\n' "$result"
printf 'backup=%s\n' "$backup/AIGC-YCDM-reclassified-logs-before.tsv"
printf 'rollback=%s\n' "$backup/rollback-history-correction.sql"
