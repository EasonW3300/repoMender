#!/bin/sh
set -eu

# Keep the repository-wide Go coverage gate intentionally modest so it catches
# regressions without pretending package-local coverage is already uniform.
coverage_file=${1:-coverage.out}
minimum=${GO_COVERAGE_MINIMUM:-35.0}

test -s "$coverage_file"
actual=$(go tool cover -func="$coverage_file" | awk '$1 == "total:" { print $3 }' | tr -d '%')
test -n "$actual"

awk -v actual="$actual" -v minimum="$minimum" 'BEGIN {
  if ((actual + 0) < (minimum + 0)) {
    printf "Go coverage %.1f%% is below the %.1f%% minimum\n", actual, minimum > "/dev/stderr"
    exit 1
  }
  printf "Go coverage %.1f%% meets the %.1f%% minimum\n", actual, minimum
}'
