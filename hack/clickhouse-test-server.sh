#!/usr/bin/env bash
#
# Start or stop a ClickHouse server for the ClickHouse integration tests.
#
# The server is configured the way the tests need it: the `default` user has a
# password, SQL-driven access control and remote access, and all four
# endpoints are enabled — native 9000, native TLS 9440, HTTP 8123 and HTTPS
# 8443 — so every connection method the driver supports can be exercised. The
# TLS certificate is self-signed, which is why the tests use the `skip-verify`
# TLS mode.
#
# Usage:
#   hack/clickhouse-test-server.sh start [--image IMAGE] [--name NAME] [--password PASSWORD]
#   hack/clickhouse-test-server.sh stop  [--name NAME]
#
# Environment:
#   CLICKHOUSE_IMAGE     container image           (default clickhouse/clickhouse-server:24.8)
#   CLICKHOUSE_NAME      container name           (default vals-clickhouse-test)
#   CLICKHOUSE_PASSWORD  password for `default`   (default clickhouse)
#   DOCKER               container CLI            (default docker)

set -euo pipefail

IMAGE="${CLICKHOUSE_IMAGE:-clickhouse/clickhouse-server:24.8}"
NAME="${CLICKHOUSE_NAME:-vals-clickhouse-test}"
PASSWORD="${CLICKHOUSE_PASSWORD:-clickhouse}"
DOCKER="${DOCKER:-docker}"
READY_TIMEOUT="${READY_TIMEOUT:-90}"

log() { printf '%s [clickhouse-test-server] %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

usage() {
    sed -n '3,22p' "$0" | sed 's/^# \{0,1\}//'
}

configure() {
    # Written after the container starts because the image's own entrypoint
    # drops files into the same directories.
    "$DOCKER" exec -i "$NAME" bash -s <<EOF
set -euo pipefail

openssl req -subj "/CN=localhost" -new -newkey rsa:2048 -days 30 -nodes -x509 \
    -keyout /etc/clickhouse-server/server.key \
    -out /etc/clickhouse-server/server.crt 2>/dev/null
chown clickhouse:clickhouse /etc/clickhouse-server/server.key /etc/clickhouse-server/server.crt

cat > /etc/clickhouse-server/config.d/zz-tls.xml <<'XML'
<clickhouse>
  <tcp_port_secure>9440</tcp_port_secure>
  <https_port>8443</https_port>
  <openSSL>
    <server>
      <certificateFile>/etc/clickhouse-server/server.crt</certificateFile>
      <privateKeyFile>/etc/clickhouse-server/server.key</privateKeyFile>
      <verificationMode>none</verificationMode>
      <cacheSessions>true</cacheSessions>
      <disableProtocols>sslv2,sslv3</disableProtocols>
    </server>
  </openSSL>
</clickhouse>
XML

# The image restricts \`default\` to localhost and leaves it without
# SQL-driven access control, neither of which suits the tests.
cat > /etc/clickhouse-server/users.d/zz-test-access.xml <<XML
<clickhouse>
  <users>
    <default>
      <password>${PASSWORD}</password>
      <access_management>1</access_management>
      <networks replace="replace"><ip>::/0</ip></networks>
    </default>
  </users>
</clickhouse>
XML
EOF
}

wait_ready() {
    local deadline=$((SECONDS + READY_TIMEOUT))
    while ((SECONDS < deadline)); do
        if "$DOCKER" exec "$NAME" clickhouse-client --password "$PASSWORD" \
            --query "SELECT 1" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    "$DOCKER" logs "$NAME" >&2 || true
    die "the server was not ready within ${READY_TIMEOUT}s"
}

start() {
    command -v "$DOCKER" >/dev/null 2>&1 || die "$DOCKER not found"

    log "removing any previous container named $NAME"
    "$DOCKER" rm -f "$NAME" >/dev/null 2>&1 || true

    log "starting $IMAGE as $NAME"
    "$DOCKER" run -d --name "$NAME" \
        -p 9000:9000 -p 9440:9440 -p 8123:8123 -p 8443:8443 \
        "$IMAGE" >/dev/null

    log "waiting for the server to accept connections"
    READY_TIMEOUT=60 wait_ready_nopassword

    log "applying the test configuration"
    configure
    "$DOCKER" restart "$NAME" >/dev/null

    log "waiting for the server to come back"
    wait_ready

    log "ready on native 9000, native TLS 9440, HTTP 8123, HTTPS 8443"
}

# Before the test configuration is applied the `default` user has no password.
wait_ready_nopassword() {
    local deadline=$((SECONDS + READY_TIMEOUT))
    while ((SECONDS < deadline)); do
        if "$DOCKER" exec "$NAME" clickhouse-client --query "SELECT 1" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    "$DOCKER" logs "$NAME" >&2 || true
    die "the server did not start within ${READY_TIMEOUT}s"
}

stop() {
    log "removing $NAME"
    "$DOCKER" rm -f "$NAME" >/dev/null 2>&1 || true
}

main() {
    local command="${1:-}"
    [[ -n "$command" ]] || { usage; exit 1; }
    shift || true

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --image) IMAGE="$2"; shift 2 ;;
            --name) NAME="$2"; shift 2 ;;
            --password) PASSWORD="$2"; shift 2 ;;
            -h|--help) usage; exit 0 ;;
            *) die "unknown option $1" ;;
        esac
    done

    case "$command" in
        start) start ;;
        stop) stop ;;
        -h|--help|help) usage ;;
        *) die "unknown command $command" ;;
    esac
}

main "$@"
