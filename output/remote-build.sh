set -eu
archive=/root/new-api-20260715-billing-history.tar
expected=F2B5480C6785B01608F27DDD3CF65BDD05FB6A9469A6172BD13971BD30419B58
actual=$(sha256sum "$archive" | awk '{print toupper($1)}')
[ "$actual" = "$expected" ] || { echo "checksum mismatch: $actual"; exit 1; }
builddir=/root/new-api-build-20260715-billing-history
case "$builddir" in /root/new-api-build-20260715-billing-history) ;; *) exit 1;; esac
rm -rf "$builddir"
mkdir -p "$builddir"
tar -xf "$archive" -C "$builddir"
cd "$builddir"
docker build --pull=false -t new-api-local:local-20260715-billing-history .
docker image inspect new-api-local:local-20260715-billing-history --format 'built={{.Id}} size={{.Size}} created={{.Created}}'
