#!/usr/bin/env bash
set -euo pipefail

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
  local uid gid
  uid="${PUID}"
  gid="${PGID}"

  # Ensure group exists with requested GID
  if getent group app >/dev/null 2>&1; then
    current_gid=$(getent group app | cut -d: -f3)
    if [ "${current_gid}" != "${gid}" ]; then
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
    useradd -u "${uid}" -g "${gid}" -m -d /home/app app || true
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
  if [ $# -gt 0 ] && [[ "$1" == -* ]]; then
    set -- passctrl "$@"
  fi

  if [ $# -eq 0 ] || [ "$1" = "passctrl" ] && [ $# -eq 1 ]; then
    # Provide a sensible default
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

  cmd=( $(build_cmd "$@") )
  log "starting as UID ${PUID}, GID ${PGID}: ${cmd[*]}"
  exec gosu app:app "${cmd[@]}"
}

main "$@"

