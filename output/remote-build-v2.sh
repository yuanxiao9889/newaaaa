set -eu
archive=/root/new-api-20260715-billing-history-v2.tar
expected=C4122BE27CF82A05E636FFCF829BD454E1EB688473F10B03EA096489AFDBF36C
actual=$(sha256sum "$archive" | awk '{print toupper($1)}')
[ "$actual" = "$expected" ] || { echo "checksum mismatch: $actual"; exit 1; }
builddir=/root/new-api-build-20260715-billing-history-v2
case "$builddir" in /root/new-api-build-20260715-billing-history-v2) ;; *) exit 1;; esac
rm -rf "$builddir"
mkdir -p "$builddir"
tar -xf "$archive" -C "$builddir"
cd "$builddir"
docker build --pull=false -t new-api-local:local-20260715-billing-history-v2 .
docker image inspect new-api-local:local-20260715-billing-history-v2 --format 'built={{.Id}} size={{.Size}} created={{.Created}}'
