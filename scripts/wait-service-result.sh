#!/usr/bin/env bash
# Source this file from the service deployment wrapper.
wait_service_result() {
# The Job owns execution. A closed observation connection is not completion.
# Keep polling after transient API errors. Only an exit record or an observed
# terminal or missing Pod ends this wait. Empty or malformed observations are
# not completion records. The Job has its own time limit.
while :; do
  # The oc request timeout does not bound an upgraded exec stream.
  if result=$(timeout --signal=TERM --kill-after=5s 45s "${oc_cmd[@]}" -n "$namespace" exec "$pod" -c test -- cat /work/deployment.exit 2>/dev/null); then
    if [[ $result =~ ^(0|[1-9][0-9]{0,2})$ ]] && ((result <= 255)); then
      return 0
    fi
  fi
  if phase=$(timeout --signal=TERM --kill-after=5s 45s "${oc_cmd[@]}" -n "$namespace" get pod "$pod" --ignore-not-found -o jsonpath='{.status.phase}' 2>/dev/null); then
    case "$phase" in
      Failed|Succeeded|'')
        timeout --signal=TERM --kill-after=5s 45s "${oc_cmd[@]}" -n "$namespace" logs "$pod" -c test > "$results/deployment.log" 2>/dev/null || true
        echo "Test Pod stopped without a completion record: ${phase:-missing}" >&2
        return 1
        ;;
    esac
  fi
  sleep 5
done
}
