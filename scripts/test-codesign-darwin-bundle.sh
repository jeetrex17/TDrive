#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WAILS3_BIN="${WAILS3_BIN:-wails3}"

fail() {
  printf 'test-codesign-darwin-bundle: %s\n' "$*" >&2
  exit 1
}

WAILS3_BIN="$(command -v "$WAILS3_BIN")" || fail "wails3 is required; set WAILS3_BIN to its path"
if [[ "$WAILS3_BIN" != /* ]]; then
  WAILS3_BIN="$(cd "$(dirname "$WAILS3_BIN")" && pwd)/$(basename "$WAILS3_BIN")"
fi
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tdrive-codesign-darwin-test.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT

stub_bin="$TMP_ROOT/stubs"
mkdir -p "$stub_bin" "$TMP_ROOT/signing inputs"
cat > "$stub_bin/codesign" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\0' "$@" >> "$CODESIGN_LOG"
EOF
cat > "$stub_bin/xcrun" <<'EOF'
#!/usr/bin/env bash
# Keep any generated iOS asset catalog outside native Xcode tools.
exit 0
EOF
chmod 755 "$stub_bin/codesign" "$stub_bin/xcrun"

# Loading the actual root Taskfile reproduces defaults from sibling platforms.
# These wrapper tasks reach the internal signing task without compiling an app.
taskfile="$TMP_ROOT/Taskfile.yml"
yaml_repo_root="${REPO_ROOT//\'/\'\'}"
cat > "$taskfile" <<EOF
version: '3'
includes:
  tdrive:
    taskfile: '$yaml_repo_root/Taskfile.yml'
    dir: '$yaml_repo_root'
tasks:
  sign:mac:
    cmds:
      - task: tdrive:darwin:codesign:bundle
        vars:
          BIN_DIR: '{{.TEST_BIN_DIR}}'
  sign:mac-identity:
    cmds:
      - task: tdrive:darwin:codesign:bundle
        vars:
          BIN_DIR: '{{.TEST_BIN_DIR}}'
          CODESIGN_IDENTITY: '{{.TEST_IDENTITY}}'
  sign:mac-provisioned:
    cmds:
      - task: tdrive:darwin:codesign:bundle
        vars:
          BIN_DIR: '{{.TEST_BIN_DIR}}'
          CODESIGN_IDENTITY: '{{.TEST_IDENTITY}}'
          ENTITLEMENTS_FILE: '{{.TEST_ENTITLEMENTS}}'
          PROVISIONING_PROFILE: '{{.TEST_PROFILE}}'
  sign:ios:
    cmds:
      - task: tdrive:ios:create:app:bundle
        vars:
          BIN_DIR: '{{.TEST_BIN_DIR}}'
          IOS_PLATFORM: device
          CODESIGN_IDENTITY: '{{.TEST_IDENTITY}}'
  sign:ios-explicit:
    cmds:
      - task: tdrive:ios:create:app:bundle
        vars:
          BIN_DIR: '{{.TEST_BIN_DIR}}'
          IOS_PLATFORM: device
          CODESIGN_IDENTITY: '{{.TEST_IDENTITY}}'
          ENTITLEMENTS_FILE: '{{.TEST_ENTITLEMENTS}}'
          PROVISIONING_PROFILE: '{{.TEST_PROFILE}}'
EOF

identity="Developer ID Application: Fixture 'Signer' (TESTTEAM)"
entitlements="$TMP_ROOT/signing inputs/fixture.entitlements"
profile="$TMP_ROOT/signing inputs/fixture.provisionprofile"
printf 'fixture entitlements\n' > "$entitlements"
printf 'fixture provisioning profile\n' > "$profile"

prepare_case() {
  case_dir="$TMP_ROOT/$1"
  app_path="$case_dir/TDrive.app"
  codesign_log="$case_dir/codesign.args"
  task_output="$case_dir/task.out"
  mkdir -p "$app_path/Contents" "$case_dir"
  # The iOS bundle task copies this executable to its temporary output bundle.
  printf 'fixture executable\n' > "$case_dir/tdrive"
}

run_task() (
  cd "$TMP_ROOT"
  env -u CODESIGN_IDENTITY -u ENTITLEMENTS_FILE -u PROVISIONING_PROFILE \
    PATH="$stub_bin:$PATH" CODESIGN_LOG="$codesign_log" \
    "$WAILS3_BIN" task "$@" \
    "TEST_BIN_DIR=$case_dir" > "$task_output" 2>&1
)

expect_success() {
  if ! run_task "$@"; then
    cat "$task_output" >&2
    fail "$(basename "$case_dir") failed"
  fi
}

expect_codesign_args() {
  printf '%s\0' "$@" > "$case_dir/expected.args"
  if ! cmp -s "$case_dir/expected.args" "$codesign_log"; then
    if [ -f "$codesign_log" ]; then
      tr '\0' '\n' < "$codesign_log" >&2
    fi
    fail "$(basename "$case_dir") passed unexpected codesign arguments"
  fi
}

expect_rejected() {
  if run_task sign:mac-provisioned "$@"; then
    fail "$(basename "$case_dir") accepted incomplete provisioned signing"
  fi
  [ ! -e "$codesign_log" ] || fail "$(basename "$case_dir") signed before rejecting inputs"
  [ ! -e "$app_path/Contents/embedded.provisionprofile" ] ||
    fail "$(basename "$case_dir") copied a profile before rejecting inputs"
}

prepare_case mac-default
expect_success sign:mac
expect_codesign_args --force --deep --sign - "$app_path"
[ ! -e "$app_path/Contents/embedded.provisionprofile" ] || fail "ad-hoc signing embedded a profile"

prepare_case mac-identity-only
expect_success sign:mac-identity "TEST_IDENTITY=$identity"
expect_codesign_args --force --deep --sign "$identity" "$app_path"

prepare_case mac-provisioned
expect_success sign:mac-provisioned "TEST_IDENTITY=$identity" \
  "TEST_ENTITLEMENTS=$entitlements" "TEST_PROFILE=$profile"
expect_codesign_args --force --deep --timestamp --sign "$identity" \
  --entitlements "$entitlements" "$app_path"
cmp -s "$profile" "$app_path/Contents/embedded.provisionprofile" || fail "profile bytes were not embedded"

prepare_case mac-missing-profile
expect_rejected "TEST_IDENTITY=$identity" "TEST_ENTITLEMENTS=$entitlements" TEST_PROFILE=

prepare_case mac-missing-entitlements
expect_rejected "TEST_IDENTITY=$identity" TEST_ENTITLEMENTS= "TEST_PROFILE=$profile"

prepare_case mac-missing-identity
expect_rejected TEST_IDENTITY=- "TEST_ENTITLEMENTS=$entitlements" "TEST_PROFILE=$profile"

prepare_case mac-invalid-profile
expect_rejected "TEST_IDENTITY=$identity" "TEST_ENTITLEMENTS=$entitlements" \
  "TEST_PROFILE=$TMP_ROOT/missing.provisionprofile"

prepare_case mac-invalid-entitlements
expect_rejected "TEST_IDENTITY=$identity" "TEST_ENTITLEMENTS=$TMP_ROOT/missing.entitlements" \
  "TEST_PROFILE=$profile"

prepare_case ios-default-entitlements
expect_success sign:ios "TEST_IDENTITY=$identity"
expect_codesign_args --force --sign "$identity" \
  --entitlements build/ios/entitlements.plist "$app_path"

prepare_case ios-explicit-entitlements
expect_success sign:ios-explicit "TEST_IDENTITY=$identity" \
  "TEST_ENTITLEMENTS=$entitlements" "TEST_PROFILE=$profile"
expect_codesign_args --force --sign "$identity" --entitlements "$entitlements" "$app_path"
cmp -s "$profile" "$app_path/embedded.mobileprovision" || fail "iOS profile bytes were not embedded"

printf 'macOS bundle signing and iOS entitlement isolation tests passed\n'
