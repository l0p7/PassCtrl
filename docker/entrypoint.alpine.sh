#!/usr/bin/env sh
set -eu

# Defaults
: "${TZ:=UTC}"
: "${PUID:=1000}"
: "${PGID:=1000}"

log() { echo "[entrypoint] $*"; }

setup_timezone() {
  if [ -n "${TZ}" ] && [ -e "/usr/share/zoneinfo/${TZ}" ]; then
    ln -snf "/usr/share/zoneinfo/${TZ}" /etc/localtime || true
    echo "${TZ}" > /etc/timezone || true
    log "timezone set to ${TZ}"
  else
    log "timezone ${TZ} not found; keeping default"
  fi
}

ensure_user() {
  uid="${PUID}"
  gid="${PGID}"

  # Ensure group exists with requested GID
  if grep -qE "^app:" /etc/group; then
    current_gid=$(grep -E "^app:" /etc/group | cut -d: -f3 | head -n1)
    if [ "${current_gid}" != "${gid}" ]; then
      # Requires shadow's groupmod
      groupmod -g "${gid}" app || true
      log "modified group 'app' to GID ${gid} (was ${current_gid})"
    fi
  else
    groupadd -g "${gid}" app || true
    log "created group 'app' with GID ${gid}"
  fi

  # Ensure user exists with requested UID and primary GID
  if id -u app >/dev/null 2>&1; then
    current_uid=$(id -u app)
    if [ "${current_uid}" != "${uid}" ]; then
      usermod -u "${uid}" app || true
      log "modified user 'app' to UID ${uid} (was ${current_uid})"
    fi
    usermod -g "${gid}" app || true
  else
    useradd -u "${uid}" -g "${gid}" -M -d /home/app -s /sbin/nologin app || true
    log "created user 'app' with UID ${uid}, GID ${gid}"
  fi

  # Ensure ownership of common bind-mounts
  for d in /config /rules /templates; do
    if [ -e "$d" ]; then
      chown -R app:app "$d" 2>/dev/null || true
    fi
  done
}

build_cmd() {
  # If first arg looks like an option, prefix with the binary
  if [ $# -gt 0 ] && [ "${1#-}" != "$1" ]; then
    set -- passctrl "$@"
  fi

  if [ $# -eq 0 ] || { [ "$1" = "passctrl" ] && [ $# -eq 1 ]; }; then
    if [ -f /config/server.yaml ]; then
      set -- passctrl --config /config/server.yaml
    else
      set -- passctrl
    fi
  fi

  echo "$@"
}

main() {
  setup_timezone
  ensure_user

  # shellcheck disable=SC2046
  set -- $(build_cmd "$@")
  log "starting as UID ${PUID}, GID ${PGID}: $*"
  exec su-exec app:app "$@"
}

main "$@"
