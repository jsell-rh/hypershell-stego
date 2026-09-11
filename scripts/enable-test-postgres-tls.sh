#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 || -z ${GITHUB_ENV:-} ]]; then
  echo 'Usage in CI: enable-test-postgres-tls.sh CONTAINER OUTPUT_DIRECTORY' >&2
  exit 2
fi
container=$1
directory=$2
umask 077
mkdir -p -- "$directory"
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -subj '/CN=localhost' -addext 'subjectAltName=DNS:localhost' \
  -keyout "$directory/server.key" -out "$directory/server.crt" 2>/dev/null
docker exec "$container" mkdir -p /var/lib/postgresql/test-tls
docker cp "$directory/server.key" "$container:/var/lib/postgresql/test-tls/server.key"
docker cp "$directory/server.crt" "$container:/var/lib/postgresql/test-tls/server.crt"
docker exec "$container" chown -R postgres:postgres /var/lib/postgresql/test-tls
docker exec "$container" chmod 600 /var/lib/postgresql/test-tls/server.key
rm -- "$directory/server.key"
docker exec -i "$container" psql -U postgres -v ON_ERROR_STOP=1 <<'SQL'
ALTER SYSTEM SET ssl_cert_file='/var/lib/postgresql/test-tls/server.crt';
ALTER SYSTEM SET ssl_key_file='/var/lib/postgresql/test-tls/server.key';
ALTER SYSTEM SET ssl='on';
SELECT pg_reload_conf();
SQL
for attempt in {1..20}; do
  if result=$(docker exec "$container" sh -c '
    export PGPASSWORD="$POSTGRES_PASSWORD" PGCONNECT_TIMEOUT=2
    exec psql "host=localhost user=postgres dbname=postgres sslmode=verify-full sslrootcert=/var/lib/postgresql/test-tls/server.crt" -At -v ON_ERROR_STOP=1 -c "SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()"
  ' 2>/dev/null) && [[ $result == t ]]; then
    printf 'STEGO_TEST_POSTGRES_CA_FILE=%s/server.crt\n' "$directory" >> "$GITHUB_ENV"
    exit 0
  fi
  sleep 1
done
echo 'The test database did not enable verified TLS.' >&2
exit 1
