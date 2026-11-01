#!/bin/bash
# Copyright 2026 ALTESSA SOLUTIONS INC.
# Use of this source code is governed by the MIT license that can be found in
# the LICENSE file.
# Fail the build when a protocol package falls below the coverage threshold.
#
# Usage: coverage-gate.sh [coverage-profile]
#
# The threshold and the package list live in .coverage.yml so the gate is
# configuration, not a magic number buried in CI.
set -e -o pipefail

PROFILE="${1:-coverage.out}"
CONFIG="${COVERAGE_CONFIG:-.coverage.yml}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

if [[ ! -f "$PROFILE" ]]; then
    echo -e "${RED}Error:${NC} coverage profile not found: $PROFILE" >&2
    echo "Generate one with: go test -race -coverprofile=$PROFILE ./..." >&2
    exit 1
fi

if [[ ! -f "$CONFIG" ]]; then
    echo -e "${RED}Error:${NC} coverage config not found: $CONFIG" >&2
    exit 1
fi

MODULE=$(go list -m)

THRESHOLD=$(awk -F':[[:space:]]*' '/^threshold:/ { print $2; exit }' "$CONFIG")
if [[ -z "$THRESHOLD" ]]; then
    echo -e "${RED}Error:${NC} no 'threshold:' key in $CONFIG" >&2
    exit 1
fi

# Read the YAML list under `packages:` — entries are `  - name` lines, and the
# list ends at the next unindented key.
PACKAGES=$(awk '
    /^packages:/                        { in_list = 1; next }
    in_list && /^[[:space:]]*#/         { next }
    in_list && /^[[:space:]]*-[[:space:]]*/ {
        sub(/^[[:space:]]*-[[:space:]]*/, "")
        print
        next
    }
    in_list && /^[^[:space:]]/          { in_list = 0 }
' "$CONFIG")

if [[ -z "$PACKAGES" ]]; then
    echo -e "${RED}Error:${NC} no packages listed in $CONFIG" >&2
    exit 1
fi

# Statement-weighted coverage of one package, computed from the raw profile.
# `go tool cover -func` reports per-function percentages, whose plain average
# would misreport a package with functions of unequal size.
package_coverage() {
    local prefix="$1"

    awk -v prefix="$prefix" '
        index($0, ".go:") == 0 { next }
        {
            colon = index($1, ":")
            file = substr($1, 1, colon - 1)
            if (index(file, prefix) != 1) next
            # Direct children only; a subpackage is gated on its own entry.
            if (index(substr(file, length(prefix) + 1), "/") != 0) next
            total += $2
            if ($3 > 0) covered += $2
        }
        END { printf "%d %d", covered + 0, total + 0 }
    ' "$PROFILE"
}

echo "========================================="
echo "Coverage gate: threshold ${THRESHOLD}%"
echo "Profile: $PROFILE"
echo "========================================="

failed=0
checked=0
skipped=0

while IFS= read -r pkg; do
    [[ -n "$pkg" ]] || continue

    read -r covered total <<<"$(package_coverage "${MODULE}/${pkg}/")"

    if [[ "$total" -eq 0 ]]; then
        printf "  ${YELLOW}skip${NC}  %-12s no statements yet\n" "$pkg"
        skipped=$((skipped + 1))
        continue
    fi

    checked=$((checked + 1))

    # Rounded for the eye, compared exactly. At one decimal place a package
    # sitting at 89.977% prints as 90.0%, and a gate that compared the string it
    # had just printed would pass it — which is the one thing this gate exists
    # to catch.
    pct=$(awk -v c="$covered" -v t="$total" 'BEGIN { printf "%.2f", c * 100 / t }')

    if awk -v c="$covered" -v t="$total" -v th="$THRESHOLD" 'BEGIN { exit !(c * 100 / t >= th + 0) }'; then
        printf "  ${GREEN}ok${NC}    %-12s %5s%% (%d/%d statements)\n" "$pkg" "$pct" "$covered" "$total"
    else
        printf "  ${RED}FAIL${NC}  %-12s %5s%% (%d/%d statements) — below ${THRESHOLD}%%\n" "$pkg" "$pct" "$covered" "$total"
        failed=$((failed + 1))
    fi
done <<<"$PACKAGES"

echo "========================================="

if [[ $failed -gt 0 ]]; then
    echo -e "${RED}${failed} package(s) below the ${THRESHOLD}% threshold${NC}"
    echo ""
    echo "A protocol package under the threshold is not a style remark: every"
    echo "protocol scenario needs a negative test alongside the positive one."
    echo "Add the missing tests rather than lowering the threshold in $CONFIG."
    exit 1
fi

echo -e "${GREEN}All ${checked} checked package(s) meet the ${THRESHOLD}% threshold${NC} (${skipped} skipped)"
