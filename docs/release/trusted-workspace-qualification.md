# Trusted read/search preview qualification

The user explicitly chose a trusted-workspace contract. This does not guarantee
isolation from other programs maliciously replacing workspace files and does not
identify every historical database copy. Existing data and SQLite WAL mode are
unchanged. No main merge or release is implied.

Commit 9cae053 introduced explicit host/session controls and native RED tests.
Run 43666475385 verified real APFS/NTFS fixture controls before six adapter
contracts failed three times on each platform because the adapters were absent.

Commit 9b14e93 implemented the adapters while retaining disabled capabilities.
Run 43667099417 passed all six adapter contracts three times on macOS 15/APFS
and Windows 2025/NTFS: 18 passes each, zero failures or skips. The full workflow
was intentionally still red because API admission refused the disabled adapter.
The Windows job used an elevated hosted-runner token; ordinary non-administrator
Windows acceptance remains unverified. This evidence must not be called a release.

After those native passes, the separate trusted capability may be explicitly
selected. The full Files bundle remains disabled on these platforms. No Apply,
Undo, Exec or MCP is added. Host --trusted-workspace requires an explicit
--workspace. Each new session must separately choose trusted_workspace=true
in default/plan mode; Web is unchecked by default. Existing sessions never gain
trust implicitly. Host-off or a changed host root revokes live read authority
while preserving stored history. API integration is separately exercised by
TestTrustedWorkspaceNativeSessionAndHostRevocation, including fresh run requests,
knowledge-file requests, exact denial causes and restored-host positive controls.

The native workflow verifies current integration results; adapter passes alone
are not a claim that API tests or all historical cross-platform regressions pass.

## Completed preview integration evidence

Commit 3f3c76e enabled only the separate explicit trusted capability after the
adapter controls passed. Its native API fixture exposed an unrelated lifecycle
error: manually created runs remained active across service recreation, so the
next CreateRun correctly returned interaction conflict. Commit 99d45e4 fixes the
fixture using the existing version-checked completion transition; no production
permission check or API denial assertion was removed. A portable four-stage
store-recreation test reproduced the incomplete-run failure before the fix.

On 99d45e4, native run 43668246293 succeeded on both macOS 15 and Windows 2025.
Jobs 132672333610 (Darwin) and 132672333475 (Windows) each passed all six adapter
contracts three times, with zero failures/skips, the ordinary production
dependency audit, and all TestTrustedWorkspace API tests three times. This now
covers initial read and knowledge-file positive controls, host-off and changed-
root exact authority refusal, and matching-host restoration positive controls.

This is a trusted Read/Search preview qualification, not an all-CI-green or
release declaration. The Windows token was elevated. The separate filtered-token
experiment remains blocked before starting its child because its candidate does
not satisfy exactly Medium integrity. It must not lower the assertion or change
machine security settings to claim ordinary-user acceptance. Existing full-Files
platform regressions and the deliberately incomplete persistent SQL VFS spike
remain outside this preview's qualified capability set.
