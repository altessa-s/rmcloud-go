# Copyright 2026 ALTESSA SOLUTIONS INC.
# Use of this source code is governed by the MIT license that can be found in
# the LICENSE file.

GOPATH         			:= ${HOME}/go
PATH           			:= ${GOPATH}/bin:$(PATH)
SHELL          			:= /bin/bash -o pipefail
.DELETE_ON_ERROR:
MAKEFLAGS      			+= --warn-undefined-variables
MAKEFLAGS      			+= --no-builtin-rules
CHANGELOG_NEXT_VERSION 	?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
MODULE         			:= github.com/altessa-s/rmcloud-go
# Directories holding a go.mod, root first. Every Go target runs in each one.
MODULES        			:= .

# dupl configuration
# - DUPL_THRESHOLD: token threshold (higher -> fewer matches)
# - DUPL_PATH: scan root
# - DUPL_IGNORE_REGEX: paths to exclude from dupl output; we treat these as generated/noise.
DUPL_THRESHOLD ?= 60
DUPL_PATH ?= .
# Beyond generated code, the gate skips test files: table-driven fixtures and
# per-package test helpers repeat by design, and tests are already excluded
# from the linter's duplicate checks for the same reason.
DUPL_IGNORE_REGEX ?= (_gen\.go|_string\.go|_test\.go|testdata/)

# gosec rule classes excluded globally. Everything else gates new code;
# reviewed false positives (G304 on caller-named files, G125 on hex-checked
# path segments) are suppressed one by one with `// #nosec <rule> -- <reason>`
# so the rule still applies everywhere else.
#   G104 - redundant with golangci-lint's errcheck (the project's error gate)
GOSEC_EXCLUDED_RULES ?= G104

# Fuzzing configuration
# - FUZZTIME: per-target budget. `go test -fuzz` accepts exactly one target per
#   package per invocation, so `make fuzz` walks them and this is the cost of a
#   single target, not of the whole sweep.
# - FUZZPKG: restricts the sweep, e.g. FUZZPKG=./internal/domain
FUZZTIME ?= 30s
FUZZPKG ?= ./...
# The recipes read both from the environment, quoted, and never splice them
# into shell source; fuzz-inputs checks them against narrow allowlists first.
export FUZZTIME FUZZPKG

.PHONY: all
all: help

# The formatters and their gci sections live in .golangci.yml, so fmt and
# lint cannot disagree. Standalone gci v0.14.0 predates Go 1.27 and files the
# new standard package uuid under default, so fmt runs golangci-lint's gci.
.PHONY: fmt
fmt: tidy  ## Run go fmt on all go files
	@cd devtools && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint
	@for m in $(MODULES); do (cd $$m && golangci-lint fmt ./...) || exit 1; done

.PHONY: lint
lint: tidy fmt golangci ## Tidy, format and run the linter

.PHONY: golangci
golangci: ## Run golangci-lint without rewriting files (as CI does)
	@cd devtools && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint
	@for m in $(MODULES); do (cd $$m && golangci-lint run ./...) || exit 1; done

.PHONY: tidy
tidy: ## Run go mod tidy
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

.PHONY: generate
generate: ## Build the pinned optgen and gci into bin/ and run go generate
	@go -C devtools build -o $(CURDIR)/bin/optgen github.com/altessa-s/go-optgen/cmd/optgen
	@go -C devtools build -o $(CURDIR)/bin/gci github.com/daixiang0/gci
	@for m in $(MODULES); do (cd $$m && PATH="$(CURDIR)/bin:$(PATH)" go generate ./...) || exit 1; done

.PHONY: vet
vet: ## Run go vet and fail when a go fix modernizer would rewrite code (as CI does)
	@for m in $(MODULES); do (cd $$m && go vet ./...) || exit 1; done
	@for m in $(MODULES); do (cd $$m && go fix -diff ./...) || { echo "go fix -diff is not empty in $$m: run go fix ./..."; exit 1; }; done

.PHONY: build
build: ## Build all packages (honors GOOS/GOARCH)
	@for m in $(MODULES); do (cd $$m && go build ./...) || exit 1; done

.PHONY: test
test: ## Run all tests
	@for m in $(MODULES); do (cd $$m && go test ./...) || exit 1; done 2>&1 | awk -F'\t' '\
	  function pad(s,w, _l,_p) { gsub(/^[ \t]+|[ \t]+$$/, "", s); _l=length(s); _p=""; while(_l++<w) _p=_p" "; return s _p } \
	  function color(s,c) { return "\033[" c "m" s "\033[0m" } \
	  { \
	    st=$$1; gsub(/^[ \t]+|[ \t]+$$/, "", st); \
	    pkg=$$2; gsub(/^[ \t]+|[ \t]+$$/, "", pkg); sub(/github.com\/altessa-s\/rmcloud-go\/?/, "", pkg); \
	    if (st=="ok") cs="32"; else if (st=="FAIL") cs="31"; else cs="33"; \
	    printf "| %s | %s | %s |\n", color(pad(st,4),cs), pad(pkg,56), pad($$3,15) \
	  }'

.PHONY: test-verbose
test-verbose: ## Run all tests with verbose output
	@for m in $(MODULES); do (cd $$m && go test -v ./...) || exit 1; done

.PHONY: test-race
test-race: ## Run all tests with the race detector (slower)
	@for m in $(MODULES); do (cd $$m && go test -race ./...) || exit 1; done

.PHONY: test-shuffle
test-shuffle: ## Run all tests with randomized order (helps catch order-dependence)
	@for m in $(MODULES); do (cd $$m && go test -shuffle=on ./...) || exit 1; done

.PHONY: test-coverage
test-coverage: ## Run tests with coverage report
	@for m in $(MODULES); do (cd $$m && go test -cover ./...) || exit 1; done

.PHONY: test-coverage-func
test-coverage-func: ## Run tests and show formatted per-package coverage
	@for m in $(MODULES); do (cd $$m && go test -cover ./...) || exit 1; done 2>&1 | awk -F'\t' '\
	  function pad(s,w, _l,_p) { gsub(/^[ \t]+|[ \t]+$$/, "", s); _l=length(s); _p=""; while(_l++<w) _p=_p" "; return s _p } \
	  function color(s,c) { return "\033[" c "m" s "\033[0m" } \
	  { \
	    st=$$1; gsub(/^[ \t]+|[ \t]+$$/, "", st); \
	    pkg=$$2; gsub(/^[ \t]+|[ \t]+$$/, "", pkg); sub(/github.com\/altessa-s\/rmcloud-go\/?/, "", pkg); \
	    if (st=="ok") cs="32"; else if (st=="FAIL") cs="31"; else cs="33"; \
	    cov=$$4; gsub(/^[ \t]+|[ \t]+$$/, "", cov); \
	    match(cov, /[0-9]+\.[0-9]+/); pct=substr(cov,RSTART,RLENGTH)+0; \
	    if (cov=="") cc="0"; else if (pct>=80) cc="32"; else if (pct>=50) cc="33"; else cc="31"; \
	    printf "| %s | %s | %s | %s |\n", color(pad(st,4),cs), pad(pkg,56), pad($$3,15), color(pad(cov,30),cc) \
	  }'

.PHONY: coverage
coverage: ## Run tests with race detector and shuffle, writing the profile to coverage.out
	@echo "mode: atomic" > coverage.out
	@for m in $(MODULES); do \
	  (cd $$m && go test -race -shuffle=on -covermode=atomic -coverprofile="$(CURDIR)/coverage.part" ./...) || { rm -f coverage.part; exit 1; }; \
	  if [ -f coverage.part ]; then tail -n +2 coverage.part >> coverage.out; rm -f coverage.part; fi; \
	done

# go tool cover cannot resolve packages of other modules from the root, so the
# total is computed from the profile: covered statements over all statements,
# printed in go tool cover's "total:" format.
.PHONY: coverage-total
coverage-total: ## Print the total statement coverage of coverage.out
	@awk 'NR > 1 { stmts += $$2; if ($$3 > 0) covered += $$2 } \
	  END { if (stmts == 0) { print "coverage-total: no statements in coverage.out" > "/dev/stderr"; exit 1 } \
	        printf "total:\t(statements)\t%.1f%%\n", covered * 100 / stmts }' coverage.out

.PHONY: test-all
test-all: coverage ## Run tests with race detector, shuffle, and coverage
	@$(MAKE) --no-print-directory coverage-total
	@rm -f coverage.out

.PHONY: coverage-gate
coverage-gate: coverage ## Fail if a protocol package is below the threshold in .coverage.yml
	@.github/scripts/coverage-gate.sh coverage.out
	@rm -f coverage.out

.PHONY: test-integration
test-integration: ## Run unit and integration tests (-tags integration) with race detector and shuffle
	@for m in $(MODULES); do (cd $$m && go test -race -shuffle=on -tags=integration ./...) || exit 1; done

.PHONY: bench
bench: ## Run all benchmarks
	@for m in $(MODULES); do (cd $$m && go test -bench=. -benchmem -run=^$$ ./...) || exit 1; done 2>&1 | awk -F'\t' '\
	  function pad(s,w, _l,_p) { gsub(/^[ \t]+|[ \t]+$$/, "", s); _l=length(s); _p=""; while(_l++<w) _p=_p" "; return s _p } \
	  function color(s,c) { return "\033[" c "m" s "\033[0m" } \
	  /^(ok|FAIL|\?)/ { \
	    st=$$1; gsub(/^[ \t]+|[ \t]+$$/, "", st); \
	    pkg=$$2; gsub(/^[ \t]+|[ \t]+$$/, "", pkg); sub(/github.com\/altessa-s\/rmcloud-go\/?/, "", pkg); \
	    if (st=="ok") cs="32"; else if (st=="FAIL") cs="31"; else cs="33"; \
	    printf "| %s | %s | %s |\n", color(pad(st,4),cs), pad(pkg,56), pad($$3,15); next \
	  } \
	  { print }'

.PHONY: bench-count
bench-count: ## Run benchmarks multiple times for benchstat (COUNT=5)
	@for m in $(MODULES); do (cd $$m && go test -bench=. -benchmem -run=^$$ -count=$${COUNT:-5} ./...) || exit 1; done | tee bench.txt
	@echo "Results saved to bench.txt (use benchstat bench.txt)"

.PHONY: bench-compare
bench-compare: ## Compare benchmarks with BASE (default origin/develop); fails on regressions above BENCH_THRESHOLD%
	@bash .github/scripts/bench-regression.sh $${BASE:-origin/develop}

.PHONY: fuzz-inputs
fuzz-inputs: # Refuse FUZZTIME and FUZZPKG outside their allowlists
	@[[ "$$FUZZTIME" =~ ^[0-9]{1,6}(ms|s|m|h|x)$$ ]] || { printf 'invalid FUZZTIME %q (a count of ms, s, m, h or x)\n' "$$FUZZTIME" >&2; exit 2; }
	@[[ "$$FUZZPKG" =~ ^\./[A-Za-z0-9_./-]*$$ ]] || { printf 'invalid FUZZPKG %q (one ./ package pattern)\n' "$$FUZZPKG" >&2; exit 2; }

# fuzz-dirs sets $$fdirs to the package directories of every module that
# FUZZPKG selects. FUZZPKG is a path relative to the repository root; a
# trailing /... selects the directory and everything below it, across module
# boundaries. Packages run from their own directory, so each uses its module.
define fuzz-dirs
want="$(CURDIR)/$${FUZZPKG#./}"; recursive=0; \
case "$$want" in */...) recursive=1; want="$${want%/...}";; esac; want="$${want%/}"; \
fdirs=""; \
for m in $(MODULES); do \
  dirs=$$(cd $$m && go list -f '{{.Dir}}' ./...) || { printf '\033[31mcannot list the packages of %s\033[0m\n' "$$m" >&2; exit 1; }; \
  for d in $$dirs; do \
    if [ "$$d" = "$$want" ]; then fdirs="$$fdirs $$d"; \
    elif [ $$recursive = 1 ]; then case "$$d/" in "$$want"/*) fdirs="$$fdirs $$d";; esac; fi; \
  done; \
done
endef

.PHONY: fuzz-list
fuzz-list: fuzz-inputs ## List every fuzz target (package and name)
	@$(fuzz-dirs); \
	for d in $$fdirs; do \
	  list=$$(cd "$$d" && go test -list '^Fuzz' .) || exit 1; \
	  rel="$${d#$(CURDIR)}"; rel="$${rel#/}"; rel="$${rel:-.}"; \
	  for t in $$(printf '%s\n' "$$list" | grep '^Fuzz' || true); do \
	    printf '%s\t%s\n' "$$rel" "$$t"; \
	  done; \
	done

.PHONY: fuzz-seed
fuzz-seed: fuzz-inputs ## Run every fuzz target over its seed corpus only (no mutation, fast)
	@$(fuzz-dirs); \
	for d in $$fdirs; do (cd "$$d" && go test -run '^Fuzz' .) || exit 1; done

.PHONY: fuzz
fuzz: fuzz-inputs ## Fuzz every target for FUZZTIME each (FUZZTIME=30s FUZZPKG=./...)
	@$(fuzz-dirs); \
	failed=""; \
	for d in $$fdirs; do \
	  rel="$${d#$(CURDIR)}"; rel="$${rel#/}"; rel="$${rel:-.}"; \
	  if ! list=$$(cd "$$d" && go test -list '^Fuzz' .); then \
	    failed="$$failed $$rel:(target discovery)"; \
	    continue; \
	  fi; \
	  for t in $$(printf '%s\n' "$$list" | grep '^Fuzz' || true); do \
	    printf '\033[36m==> %s %s (%s)\033[0m\n' "$$rel" "$$t" "$$FUZZTIME"; \
	    (cd "$$d" && go test -run '^$$' -fuzz "^$$t$$" -fuzztime "$$FUZZTIME" .) || failed="$$failed $$rel:$$t"; \
	  done; \
	done; \
	if [ -n "$$failed" ]; then \
	  printf '\033[31mfailing fuzz targets:%s\033[0m\n' "$$failed"; \
	  printf 'minimized reproducers were written to the package testdata/fuzz directories; commit them as regression seeds\n'; \
	  exit 1; \
	fi

# dupl-scan drops the ignored files before dupl sees them (filtering its
# output instead would hide a clone group whose printed pairs all involve an
# ignored file), then scans the rest. Listing the files or scanning them
# failing fails the recipe (exit 2); dupl reports a file it cannot read or
# parse on stderr and still exits 0, so any diagnostic is a failed scan too.
# A filter keeping no file (grep status 1) is a clean result, only grep
# statuses above 1 are errors.
define dupl-scan
files="$$(find "$(DUPL_PATH)" -type f -name '*.go')" || { echo "cannot list the Go files of $(DUPL_PATH)" >&2; exit 2; }; \
kept="$$(printf '%s\n' "$$files" | grep -Ev "$(DUPL_IGNORE_REGEX)")"; status=$$?; \
if [ "$$status" -gt 1 ]; then echo "dupl file filter failed" >&2; exit 2; fi; \
out=""; \
if [ -n "$$kept" ]; then \
  diag="$$(mktemp)" || exit 2; \
  out="$$(printf '%s\n' "$$kept" | dupl -files -t "$(DUPL_THRESHOLD)" -plumbing 2>"$$diag")" || { cat "$$diag" >&2; rm -f "$$diag"; echo "dupl failed" >&2; exit 2; }; \
  if [ -s "$$diag" ]; then cat "$$diag" >&2; rm -f "$$diag"; echo "dupl could not scan every file" >&2; exit 2; fi; \
  rm -f "$$diag"; \
fi
endef

.PHONY: dupl
dupl: ## Run dupl (ignoring generated files)
	@cd devtools && go install github.com/mibk/dupl
	@$(dupl-scan); \
	if [ -n "$$out" ]; then printf '%s\n' "$$out"; fi

.PHONY: dupl-check
dupl-check: ## Fail if dupl finds duplicates (ignoring generated files)
	@cd devtools && go install github.com/mibk/dupl
	@$(dupl-scan); \
	if [ -n "$$out" ]; then \
		printf '%s\n' "$$out"; \
		exit 1; \
	fi

.PHONY: ci
ci: lint vet coverage-gate test-integration security-scan dupl-check ## Run full CI checks locally (lint, vet, coverage gate, integration, security, dupl)

.PHONY: clean
clean: ## Remove generated artifacts (coverage.out, bench.txt, bin/)
	@rm -f coverage.out bench.txt
	@rm -rf bin

.PHONY: copyright
copyright: ## Add copyright header to all files
	@echo "Updating copyright header to all go files"
	@cd devtools && go install github.com/altessa-s/go-copyright-checker
	go-copyright-checker check --fix .

.PHONY: release
release: ## Semantic versioning - create and push a new release tag
	@if [ -z "$(VERSION)" ]; then \
		echo "Usage: make release VERSION=v1.2.3"; \
		echo "Current version: $$(git describe --tags --abbrev=0 2>/dev/null || echo 'no tags')"; \
		echo "Next version suggestion: $(CHANGELOG_NEXT_VERSION)"; \
		exit 1; \
	fi
	@echo "Creating release $(VERSION)..."
	@git tag -a $(VERSION) -m "Release $(VERSION)"
	@git push origin $(VERSION)
	@echo "Release $(VERSION) created and pushed"

.PHONY: gosec
gosec: ## Run gosec static security scanner (see GOSEC_EXCLUDED_RULES)
	@cd devtools && go install github.com/securego/gosec/v2/cmd/gosec
	@for m in $(MODULES); do (cd $$m && gosec -quiet -exclude-generated -exclude=$(GOSEC_EXCLUDED_RULES) ./...) || exit 1; done

.PHONY: govulncheck
govulncheck: ## Run govulncheck
	@cd devtools && go install golang.org/x/vuln/cmd/govulncheck
	@for m in $(MODULES); do (cd $$m && govulncheck ./...) || exit 1; done

.PHONY: security-scan
security-scan: ## Security checks - run vulnerability and security scanners
	@cd devtools && go install golang.org/x/vuln/cmd/govulncheck
	@cd devtools && go install github.com/securego/gosec/v2/cmd/gosec
	@# Run every scanner even when an earlier one reports findings, then fail
	@# if any did, so one report never hides the other.
	@status=0; \
	echo "Running Go vulnerability check ($$(go env GOVERSION))..."; \
	for m in $(MODULES); do (cd $$m && govulncheck ./...) || status=1; done; \
	echo ""; \
	echo "Running gosec security scanner..."; \
	for m in $(MODULES); do (cd $$m && gosec -quiet -exclude-generated -exclude=$(GOSEC_EXCLUDED_RULES) ./...) || status=1; done; \
	echo ""; \
	if [ $$status -ne 0 ]; then echo "Security scan found issues"; exit 1; fi; \
	echo "Security scan completed"

.PHONY: help
# Absolutely awesome: http://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
