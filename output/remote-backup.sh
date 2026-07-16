set -eu
ts=$(date +%Y%m%d-%H%M%S)
backup="/root/newapi-deploy-backups/billing-history-$ts"
workdir=/www/dk_project/dk_app/newapi/newapi_isGp
app=newapi_isgp-new-api-1
mysqlc=newapi_isgp-mysql-1
mkdir -p "$backup"
cp -a "$workdir/docker-compose.yml" "$backup/"
cp -a "$workdir/.env" "$backup/"
docker inspect "$app" > "$backup/new-api-container-inspect.json"
docker inspect "$mysqlc" > "$backup/mysql-container-inspect.json"
current_image=$(docker inspect "$app" --format '{{.Config.Image}}')
rollback_tag="new-api-local:rollback-$ts"
docker tag "$current_image" "$rollback_tag"
printf '%s\n' "$current_image" > "$backup/current-image.txt"
printf '%s\n' "$rollback_tag" > "$backup/rollback-image.txt"
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw -uroot "$db" -e "SELECT id,username,email,quota,used_quota,request_count,status,role,created_at,deleted_at FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM'; SELECT id,user_id,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,used_quota,deleted_at FROM tokens WHERE user_id IN (SELECT id FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM') ORDER BY id; SELECT type,token_id,channel_id,COUNT(*) rows_count,COALESCE(SUM(quota),0) quota_sum FROM logs WHERE user_id IN (SELECT id FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM') GROUP BY type,token_id,channel_id ORDER BY type,token_id,channel_id; SELECT status,channel_id,COALESCE(JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.token_id')),'0') token_id,COUNT(*) rows_count,COALESCE(SUM(quota),0) quota_sum FROM tasks WHERE user_id IN (SELECT id FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM') GROUP BY status,channel_id,token_id ORDER BY status,channel_id,token_id;" > "$backup/AIGC-YCDM-before.tsv"
docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysqldump --single-transaction --quick --routines --events --triggers --set-gtid-purged=OFF -uroot "$db" | gzip -1 > "$backup/$db.sql.gz"
sha256sum "$backup/$db.sql.gz" > "$backup/$db.sql.gz.sha256"
du -sh "$workdir/mysql_data" "$backup" > "$backup/sizes.txt"
printf 'BACKUP_DIR=%s\n' "$backup"
printf 'CURRENT_IMAGE=%s\n' "$current_image"
printf 'ROLLBACK_TAG=%s\n' "$rollback_tag"
cat "$backup/sizes.txt"
cat "$backup/$db.sql.gz.sha256"
