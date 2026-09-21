#!/usr/bin/env bash
# Record fixed phase names. Do not record commands, arguments, or environment data.
check_phase=setup
check_phase_file=
check_phase_event() {
  printf 'stego-check %s %s %s %s\n' "$check_scope" "$check_phase" "$1" "$2" >&2
  if [[ -n $check_phase_file ]]; then
    printf '%s\t%s\t%s\t%s\n' "$EPOCHSECONDS" "$check_phase" "$1" "$2" >> "$check_phase_file"
  fi
}
check_phase_exit() {
  local status=$?
  trap - EXIT
  check_phase_event exit "$status" || { if (( status == 0 )); then status=1; fi; }
  exit "$status"
}
check_phase_start() {
  [[ $1 =~ ^[a-z][a-z0-9-]*$ ]]
  check_phase=$1
  check_phase_event start 0
}
check_phase_done() {
  check_phase_event complete 0
}
check_phases_init() {
  [[ $1 =~ ^[a-z][a-z0-9-]*$ ]]
  check_scope=$1
  if [[ -n ${STEGO_CHECK_PHASE_ROOT:-} ]]; then
    [[ $STEGO_CHECK_PHASE_ROOT == /* ]]
    mkdir -p -m 700 -- "$STEGO_CHECK_PHASE_ROOT"
    check_phase_file=$(mktemp "$STEGO_CHECK_PHASE_ROOT/$check_scope.XXXXXXXX.tsv")
  fi
  trap check_phase_exit EXIT
  check_phase_start setup
}
