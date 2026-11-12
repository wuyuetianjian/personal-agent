# Filtered non-elevated qualification experiment

No account, ACL, UAC, privilege grants or integrity-label changes. Current-token
candidates only: qualified current, qualified linked limited, then
CreateRestrictedToken(DISABLE_MAX_PRIVILEGE | LUA_TOKEN) with Administrators
SID disabled. Every candidate and actual child must have Elevation=0, default or
limited elevation type, exactly Medium IL, Administrators absent or deny-only
(not enabled), and no enabled privilege except SeChangeNotifyPrivilege.
Queries fail closed. A restricted token may retain High IL; that is BLOCKED,
not permission to lower IL, enable a privilege or change machine configuration.
This is not evidence of a separate standard-user account.

Build the test executable with `nativeprobe_non_elevated_guard` in addition to
existing identity/lease/permit tags. Stage it in the launching user's TEMP.
TestMain checks the actual process token before any test starts. The marker is
only routing defense, never authority: forging it cannot bypass token inspection.

Example orchestration (workflow must preserve both pipeline exit statuses):

    launcher.exe -exe <TEMP>/probe.test.exe -- -test.v=test2json -test.count=3 -test.run=<exact contracts>

Pipe stdout through the toolchain's official `go tool test2json -p
agent/internal/nativefs/windowsprobe` and the existing exact-event manifest gate.
Capture stderr separately. No printed marker or PASS line replaces test events.
Require TOKEN_GUARD_OK in addition to, not instead of, all expected test events.

The launcher uses explicit environment allowlist and dedicated stdin/stdout/stderr
pipes. Go's Windows process implementation uses an explicit HANDLE_LIST; no
AdditionalInheritedHandles, alternate parent process or ambient storage handles.
CreateProcessAsUser failure is a blocked qualification, not a request for broader
rights. Run this experiment with a job timeout, not on untrusted executables.
Native execution remains unverified until the dedicated Windows workflow runs.

There is no explicit AdjustTokenPrivileges call. Per the documented Windows API
semantics, CreateProcessAsUser may temporarily enable caller privileges it already
possesses. This launcher grants no new privilege and never changes system policy
or retries by enabling rights after failure.
