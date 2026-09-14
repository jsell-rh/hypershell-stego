#!/bin/sh
# Run inside the bounded CI browser container. Record counts, not process data.
set -eu

output=/tmp/stego-browser-resource-peaks.txt
tmp_peak=0
shm_peak=0
fd_peak=0
sample=0
file_limits=$(awk '/^Max open files/ {print $4 " " $5}' /proc/1/limits)
while [ "$sample" -lt 1800 ]; do
  tmp_used=$(df -Pk /tmp | awk 'NR == 2 {print $3}')
  shm_used=$(df -Pk /dev/shm | awk 'NR == 2 {print $3}')
  if [ "$tmp_used" -gt "$tmp_peak" ]; then tmp_peak=$tmp_used; fi
  if [ "$shm_used" -gt "$shm_peak" ]; then shm_peak=$shm_used; fi
  readable=0
  for directory in /proc/[0-9]*/fd; do
    [ -r "$directory" ] || continue
    set -- "$directory"/*
    if [ ! -e "$1" ] && [ ! -L "$1" ]; then continue; fi
    readable=$((readable + 1))
    if [ "$#" -gt "$fd_peak" ]; then fd_peak=$#; fi
  done
  sample=$((sample + 1))
  {
    printf 'samples %s\ninterval_seconds 1\n' "$sample"
    printf 'sampled_tmp_peak_kib %s\nsampled_shm_peak_kib %s\n' "$tmp_peak" "$shm_peak"
    printf 'sampled_process_fd_peak %s\nreadable_processes_last_sample %s\n' "$fd_peak" "$readable"
    printf 'driver_open_files_soft_hard %s\n' "$file_limits"
  } > "$output.next"
  mv "$output.next" "$output"
  sleep 1
done
