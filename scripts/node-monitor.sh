#!/bin/sh
set -eu

: "${TINYMON_URL:?TINYMON_URL is required}"
: "${TINYMON_API_KEY:?TINYMON_API_KEY is required}"
: "${CLUSTER_NAME:?CLUSTER_NAME is required}"
: "${NODE_NAME:?NODE_NAME is required}"
: "${INTERVAL:=60}"

HOST_ADDRESS="k8s://${CLUSTER_NAME}/nodes/${NODE_NAME}"

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

# Format bytes to human-readable (Gi / Mi)
fmt_bytes() {
  local bytes=$1
  if [ "$bytes" -ge 1073741824 ]; then
    awk "BEGIN { printf \"%.1f Gi\", $bytes / 1073741824 }"
  else
    awk "BEGIN { printf \"%.0f Mi\", $bytes / 1048576 }"
  fi
}

collect_disk() {
  # Parse host mounts from /host/proc/mounts to find real filesystems
  local skip_fs="tmpfs|devtmpfs|overlay|squashfs|iso9660|proc|sysfs|cgroup|cgroup2|autofs|securityfs|pstore|debugfs|tracefs|fusectl|configfs|devpts|mqueue|hugetlbfs|bpf|nsfs|fuse.lxcfs|binfmt_misc|shm"

  # Extract device + mountpoint, deduplicate by device (first mount wins = shortest path)
  grep -vE "^[^ ]+ [^ ]+ ($skip_fs) " /host/proc/mounts 2>/dev/null \
    | awk '{print $1, $2}' \
    | sort -k1,1 -k2,2 \
    | awk '!seen[$1]++' \
    | while IFS=' ' read -r device host_mount; do
        # Skip non-absolute mount paths
        case "$host_mount" in /*) ;; *) continue ;; esac

        # The actual path inside the container
        local container_path="/host${host_mount}"
        [ -d "$container_path" ] || continue

        # Use df on the container path
        local df_line
        df_line=$(df -k "$container_path" 2>/dev/null | tail -n 1) || continue

        local total=$(echo "$df_line" | awk '{print $2}')
        local used=$(echo "$df_line" | awk '{print $3}')

        # Skip if total is 0 or not a number
        [ "$total" -gt 0 ] 2>/dev/null || continue

        local pct_raw=$(awk "BEGIN { printf \"%.0f\", $used / $total * 100 }")

        local total_bytes=$((total * 1024))
        local used_bytes=$((used * 1024))
        local total_h=$(fmt_bytes $total_bytes)
        local used_h=$(fmt_bytes $used_bytes)

        local status="ok"
        if [ "$pct_raw" -ge 90 ]; then status="critical"
        elif [ "$pct_raw" -ge 80 ]; then status="warning"
        fi

        local display_mount="$host_mount"
        local config=$(jq -cn --arg m "$display_mount" '{mount: $m}')
        echo $(jq -cn \
          --arg ha "$HOST_ADDRESS" \
          --arg ct "disk" \
          --arg st "$status" \
          --argjson v "$pct_raw" \
          --arg msg "${pct_raw}% used (${used_h} / ${total_h})" \
          --argjson cfg "$config" \
          '{host_address: $ha, check_type: $ct, status: $st, value: $v, message: $msg, config: $cfg}')
      done
}

collect_memory() {
  local meminfo="/host/proc/meminfo"
  if [ ! -f "$meminfo" ]; then
    jq -cn \
      --arg ha "$HOST_ADDRESS" \
      '{host_address: $ha, check_type: "memory", status: "unknown", message: "/proc/meminfo not available"}'
    return
  fi

  local total_kb=$(awk '/^MemTotal:/ {print $2}' "$meminfo")
  local avail_kb=$(awk '/^MemAvailable:/ {print $2}' "$meminfo")

  if [ -z "$total_kb" ] || [ -z "$avail_kb" ] || [ "$total_kb" -eq 0 ]; then
    jq -cn \
      --arg ha "$HOST_ADDRESS" \
      '{host_address: $ha, check_type: "memory", status: "unknown", message: "Cannot parse meminfo"}'
    return
  fi

  local used_kb=$((total_kb - avail_kb))
  local pct=$(awk "BEGIN { printf \"%.1f\", $used_kb / $total_kb * 100 }")
  local pct_int=${pct%.*}
  local total_bytes=$((total_kb * 1024))
  local used_bytes=$((used_kb * 1024))
  local total_h=$(fmt_bytes $total_bytes)
  local used_h=$(fmt_bytes $used_bytes)

  local status="ok"
  if [ "$pct_int" -ge 90 ]; then status="critical"
  elif [ "$pct_int" -ge 80 ]; then status="warning"
  fi

  jq -cn \
    --arg ha "$HOST_ADDRESS" \
    --arg st "$status" \
    --argjson v "$pct_int" \
    --arg msg "${pct}% used (${used_h} / ${total_h})" \
    '{host_address: $ha, check_type: "memory", status: $st, value: $v, message: $msg}'
}

collect_load() {
  local loadavg="/host/proc/loadavg"
  if [ ! -f "$loadavg" ]; then
    jq -cn \
      --arg ha "$HOST_ADDRESS" \
      '{host_address: $ha, check_type: "load", status: "unknown", message: "/proc/loadavg not available"}'
    return
  fi

  local load1=$(awk '{print $1}' "$loadavg")
  local load5=$(awk '{print $2}' "$loadavg")
  local load15=$(awk '{print $3}' "$loadavg")

  # Get number of CPUs
  local ncpu=$(grep -c '^processor' /host/proc/cpuinfo 2>/dev/null || echo 1)
  local pct=$(awk "BEGIN { printf \"%.0f\", $load1 / $ncpu * 100 }")

  local status="ok"
  if [ "$pct" -ge 90 ]; then status="critical"
  elif [ "$pct" -ge 80 ]; then status="warning"
  fi

  jq -cn \
    --arg ha "$HOST_ADDRESS" \
    --arg st "$status" \
    --argjson v "$pct" \
    --arg msg "Load ${load1} / ${load5} / ${load15} (1/5/15 min, ${ncpu} cores)" \
    '{host_address: $ha, check_type: "load", status: $st, value: $v, message: $msg}'
}

collect_disk_health() {
  local results=""
  for dev in /host/sys/block/sd* /host/sys/block/nvme* /host/sys/block/mmcblk*; do
    [ -e "$dev" ] || continue
    local devname=$(basename "$dev")
    local devpath="/dev/${devname}"

    # Run smartctl with JSON output
    local smart_json
    smart_json=$(smartctl -jH "$devpath" 2>/dev/null) || true

    if [ -z "$smart_json" ]; then
      echo $(jq -cn \
        --arg ha "$HOST_ADDRESS" \
        --arg dev "$devname" \
        --argjson cfg "$(jq -cn --arg d "$devname" '{device: $d}')" \
        '{host_address: $ha, check_type: "disk_health", status: "unknown", message: "S.M.A.R.T. not available", config: $cfg}')
      continue
    fi

    local passed=$(echo "$smart_json" | jq -r '.smart_status.passed // empty' 2>/dev/null)
    local temp=$(echo "$smart_json" | jq -r '.temperature.current // empty' 2>/dev/null)

    local status="unknown"
    local msg="S.M.A.R.T. not supported"
    local value="null"

    if [ "$passed" = "true" ]; then
      status="ok"
      msg="PASSED"
    elif [ "$passed" = "false" ]; then
      status="critical"
      msg="FAILED"
    fi

    if [ -n "$temp" ] && [ "$temp" != "null" ]; then
      value="$temp"
      if [ "$msg" != "S.M.A.R.T. not supported" ]; then
        msg="${msg}, ${temp}°C"
      fi
    fi

    echo $(jq -cn \
      --arg ha "$HOST_ADDRESS" \
      --arg st "$status" \
      --argjson v "$value" \
      --arg msg "$msg" \
      --argjson cfg "$(jq -cn --arg d "$devname" '{device: $d}')" \
      '{host_address: $ha, check_type: "disk_health", status: $st, value: $v, message: $msg, config: $cfg}')
  done
}

push_results() {
  local payload="$1"
  local http_code
  http_code=$(curl -s -o /dev/null -w '%{http_code}' \
    -X POST "${TINYMON_URL}/api/push/bulk" \
    -H "Authorization: Bearer ${TINYMON_API_KEY}" \
    -H "Content-Type: application/json" \
    -d "$payload")

  if [ "$http_code" -ge 200 ] && [ "$http_code" -lt 300 ]; then
    log "Push OK (HTTP ${http_code})"
  else
    log "Push FAILED (HTTP ${http_code})"
  fi
}

log "Starting node-monitor for ${NODE_NAME} in cluster ${CLUSTER_NAME}"
log "TinyMon URL: ${TINYMON_URL}"
log "Interval: ${INTERVAL}s"

while true; do
  log "Collecting metrics..."

  # Collect all results into a temp file
  TMPFILE=$(mktemp)

  collect_disk >> "$TMPFILE"
  collect_memory >> "$TMPFILE"
  collect_load >> "$TMPFILE"
  collect_disk_health >> "$TMPFILE"

  # Build JSON array from collected lines
  RESULT_COUNT=$(wc -l < "$TMPFILE" | tr -d ' ')

  if [ "$RESULT_COUNT" -gt 0 ]; then
    PAYLOAD=$(jq -cs '{results: .}' "$TMPFILE")
    log "Pushing ${RESULT_COUNT} results..."
    push_results "$PAYLOAD"
  else
    log "No results collected"
  fi

  rm -f "$TMPFILE"
  sleep "$INTERVAL"
done
