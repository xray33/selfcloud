#!/usr/bin/env bash
# Scans Go packages for maintainability issues:
# - Code duplication & repeated magic strings (dupl, goconst)
# - Cyclomatic & cognitive complexity (gocyclo, gocognit, nestif, funlen)
# - Go clean code idioms & error handling (gocritic, revive, errorlint, unparam, perfsprint)
#
# Usage:
#   ./.agents/skills/go-maintainability-review/scripts/scan_maintainability.sh [target_packages...] [--include-tests]

set -uo pipefail

export PATH="${HOME}/.goenv/shims:/opt/homebrew/bin:/usr/local/bin:${PATH}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILL_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${SKILL_DIR}/../../.." && pwd)"
CONFIG_FILE="${SKILL_DIR}/resources/golangci-maintainability.yml"

INCLUDE_TESTS="false"
TARGETS=()

for arg in "$@"; do
  if [[ "$arg" == "--include-tests" ]]; then
    INCLUDE_TESTS="true"
  else
    TARGETS+=("$arg")
  fi
done

if [[ ${#TARGETS[@]} -eq 0 ]]; then
  TARGETS=("./internal/..." "./cmd/...")
fi

cd "${REPO_ROOT}" || exit 1

if ! command -v golangci-lint >/dev/null 2>&1; then
  echo "ERROR: golangci-lint is not installed or not in PATH." >&2
  exit 1
fi

echo "================================================================================"
echo "SelfCloud Go Maintainability Scan"
echo "Targets       : ${TARGETS[*]}"
echo "Include Tests : ${INCLUDE_TESTS}"
echo "Config        : ${CONFIG_FILE#${REPO_ROOT}/}"
echo "================================================================================"
echo ""

# Run golangci-lint with maintainability config and normalize relative paths
RAW_OUTPUT=$(golangci-lint run \
  -c "${CONFIG_FILE}" \
  --tests="${INCLUDE_TESTS}" \
  "${TARGETS[@]}" 2>&1 || true)

OUTPUT=$(echo "${RAW_OUTPUT}" | sed -E 's#^(\.\./)+##')

if [[ -z "${OUTPUT}" ]]; then
  echo "No maintainability issues detected by static analysis."
  exit 0
fi

echo "### 1. Cyclomatic & Cognitive Complexity Hotspots (gocyclo, gocognit, funlen, nestif)"
echo "--------------------------------------------------------------------------------"
echo "${OUTPUT}" | grep -E '\((gocyclo|gocognit|funlen|nestif)\)$' | sort -t: -k1,1 -k2,2n || echo "None"
echo ""

echo "### 2. Copy-Paste & Repeated Constants (dupl, goconst)"
echo "--------------------------------------------------------------------------------"
echo "${OUTPUT}" | grep -E '\((dupl|goconst)\)$' || echo "None"
echo ""

echo "### 3. Go Clean Code, Idioms & Dead Parameters (gocritic, revive, errorlint, unparam, perfsprint, ineffassign, unused)"
echo "--------------------------------------------------------------------------------"
echo "${OUTPUT}" | grep -E '\((gocritic|revive|errorlint|unparam|perfsprint|ineffassign|unused)\)$' | sort -t: -k1,1 -k2,2n || echo "None"
echo ""

echo "### 4. Issue Counts by Linter"
echo "--------------------------------------------------------------------------------"
echo "${OUTPUT}" | grep -E '^([0-9]+ issues:|\* [a-z0-9]+: [0-9]+)' || echo "None"
