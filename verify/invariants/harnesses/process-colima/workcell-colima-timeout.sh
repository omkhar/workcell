run_host_colima() {
  sleep 60
}

start_epoch="$(date +%s)"
if run_host_colima_with_timeout 1 delete --profile timeout-fixture; then
  echo "Expected run_host_colima_with_timeout to time out for a hung colima command" >&2
  exit 1
else
  status=$?
fi
elapsed=$(( $(date +%s) - start_epoch ))
[[ "${status}" -eq 124 ]]
[[ "${elapsed}" -lt 15 ]]
