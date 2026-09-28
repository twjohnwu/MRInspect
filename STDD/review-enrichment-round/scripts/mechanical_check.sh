#!/usr/bin/env bash

# Generated STDD mechanical gate. Run this script FROM THE CHANGE DIRECTORY:
#   bash scripts/mechanical_check.sh [expected-passed-test-count]
#
# The generator replaces the {{...}} values below. The first argument overrides
# 1 when supplied. MECH_LOG defaults to
# $TMPDIR/stdd-mechanical-check.log (or /tmp when TMPDIR is unset).

set -u

ledger='scripts/test-fingerprints.sha256'
allowlist='scripts/scope-allowlist.txt'
spec_file='spec.md'
design_file=''
test_command='cd ../.. && go test ./internal/config/ ./internal/enrich/ ./internal/ai/ ./internal/rag/intake/ ./internal/reviewer/ -count=1 -v'
generated_expected='60'
expected_passed=${1-$generated_expected}
log_file=${MECH_LOG:-${TMPDIR:-/tmp}/stdd-mechanical-check.log}
overall_status=0

append_log() {
    printf '\n[%s]\n%s\n' "$1" "$2" >> "$log_file"
}

mark_failure() {
    overall_status=1
}

# 1. Locked test-file fingerprints. The ledger is shasum -c compatible and
# must be generated after RED by the dispatched ledger-writing agent.
fingerprint_output=$(shasum -a 256 -c "$ledger" 2>&1)
if [ $? -eq 0 ]; then
    printf 'MECH fingerprints: PASS\n'
else
    printf 'MECH fingerprints: FAIL — checksum mismatch\n'
    append_log fingerprints "$fingerprint_output"
    mark_failure
fi

# 2. Test-suite result. The generator supplies an AWK PROGRAM, not merely a
# regex, in TEST_COUNT_PATTERN. It MUST print exactly three whitespace-separated
# integers at END: passed_count failed_count result_lines. Swap this program for
# each runner (pytest, go test, node --test, etc.) while preserving that contract.
test_count_awk='/^--- PASS: /{p++} /^--- FAIL: /{f++} END{print p+0, f+0, p+f}'
test_output=$(bash -c "$test_command" 2>&1)
test_command_status=$?
test_summary=$(printf '%s\n' "$test_output" | awk "$test_count_awk")
set -- $test_summary
passed_count=${1-0}
failed_count=${2-0}
result_lines=${3-0}

if [ "$test_command_status" -ne 0 ] && [ "$failed_count" -eq 0 ]; then
    printf 'MECH tests: FAIL — test command exited %s\n' "$test_command_status"
    append_log tests "$test_output"
    mark_failure
elif [ "$result_lines" -eq 0 ]; then
    printf 'MECH tests: FAIL — no parseable test result lines\n'
    append_log tests "$test_output"
    mark_failure
elif [ "$failed_count" -gt 0 ]; then
    printf 'MECH tests: FAIL — %s failed\n' "$failed_count"
    append_log tests "$test_output"
    mark_failure
elif [ -n "$expected_passed" ] && [ "$passed_count" != "$expected_passed" ]; then
    printf 'MECH tests: FAIL — expected %s passed, got %s\n' "$expected_passed" "$passed_count"
    append_log tests "$test_output"
    mark_failure
else
    printf 'MECH tests: PASS\n'
fi

# 3. Tracked working-tree modifications must be allowlisted when a list exists.
if [ -f "$allowlist" ]; then
    status_output=$(git status --porcelain 2>&1)
    if [ $? -ne 0 ]; then
        printf 'MECH scope: FAIL — git status failed\n'
        append_log scope "$status_output"
        mark_failure
    else
        scope_extra=$(printf '%s\n' "$status_output" | awk '
            NR == FNR { allowed[$0] = 1; next }
            substr($0, 1, 3) == " M " {
                path = substr($0, 4)
                if (!(path in allowed)) print path
            }
        ' "$allowlist" -)
        if [ -n "$scope_extra" ]; then
            printf 'MECH scope: FAIL — unallowlisted tracked modifications\n'
            append_log scope "$scope_extra"
            mark_failure
        else
            printf 'MECH scope: PASS\n'
        fi
    fi
else
    printf 'MECH scope: PASS\n'
fi

# 4. Approved fingerprints cover document bodies after the second frontmatter
# fence. spec.md is mandatory. An empty DESIGN_UX_PATH deliberately skips only
# the design-ux half, for changes that have no design-ux.md.
body_hash() {
    awk 'found { print } $0 == "---" { if (++fences == 2) found = 1 }' "$1" |
        shasum -a 256 |
        awk '{ print $1 }'
}

frontmatter_value() {
    awk -v key="$2" '
        $0 ~ ("^" key ":[[:space:]]*") {
            value = $0
            sub("^[^:]*:[[:space:]]*", "", value)
            gsub(/^"|"$/, "", value)
            print value
            exit
        }
    ' "$1"
}

if [ ! -f "$spec_file" ]; then
    printf 'MECH spec: FAIL — spec file missing\n'
    append_log spec "missing: $spec_file"
    mark_failure
else
    actual_spec=$(body_hash "$spec_file")
    approved_spec=$(frontmatter_value "$spec_file" approved_fingerprint)
    spec_problem=''
    if [ -z "$approved_spec" ]; then
        spec_problem='approved_fingerprint missing'
    elif [ "$actual_spec" != "$approved_spec" ]; then
        spec_problem='spec body fingerprint mismatch'
    fi

    if [ -n "$design_file" ]; then
        if [ ! -f "$design_file" ]; then
            spec_problem="${spec_problem:+$spec_problem; }design-ux file missing"
        else
            actual_design=$(body_hash "$design_file")
            approved_design=$(frontmatter_value "$spec_file" design_ux_fingerprint)
            if [ -z "$approved_design" ]; then
                spec_problem="${spec_problem:+$spec_problem; }design_ux_fingerprint missing"
            elif [ "$actual_design" != "$approved_design" ]; then
                spec_problem="${spec_problem:+$spec_problem; }design-ux body fingerprint mismatch"
            fi
        fi
    fi

    if [ -n "$spec_problem" ]; then
        printf 'MECH spec: FAIL — %s\n' "$spec_problem"
        append_log spec "spec=$spec_file approved=$approved_spec actual=$actual_spec\ndesign=$design_file"
        mark_failure
    else
        printf 'MECH spec: PASS\n'
    fi
fi

if [ "$overall_status" -ne 0 ]; then
    printf 'MECH log: %s\n' "$log_file"
fi

exit "$overall_status"
