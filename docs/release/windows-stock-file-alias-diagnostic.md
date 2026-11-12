# Hostile stock file-alias diagnostic

Synthetic temporary data only. This is deliberately hostile SQLite multi-path
access, NOT a normal SQLite compatibility requirement. No production bridge,
existing epoch test, ACL, account, database format or journal mode is changed.

One exact parent test runs three times. It reuses the proven physical stock-owner
fixture, checkpoints a unique synthetic payload into the main database, ends all
statements, and retains the owner. Native exclusive probes must conflict with
main SHARED and SHM DMS. A new different-basename file symlink must be a real
symlink, and a separate native follow-open must prove its target is the original
single-link main identity. These are setup controls, never security refusals.

An independent stock child reads journal mode without changing it, SELECTs the
checkpointed payload, attempts INSERT SELECT of that payload and verifies the
commit. It closes normally. The parent requires actual process completion and
exactly one structured result. Only a typed SQLite error with its native code
counts as an observed SQL rejection. Child setup/panic/timeout, unexpected result,
non-SQLite failure or close uncertainty fails the diagnostic.

The canonical owner must retain all three identities and both locks. The test
records its post-alias visible row count rather than assuming separate caches or
shared state. It records alias WAL/SHM existence and WAL size after child close.
A successful SQL operation without the expected payload-bearing WAL is not
silently called protected. Result branches are mutually exclusive:

- `STOCK_ALIAS_GAP_CONFIRMED`: a fresh, qualified alias WAL contains the synthetic
  payload and the real Root.read primitive returns it with only original three
  owner IDs excluded. Pin/register the same alias WAL identity and require
  errProtected with zero bytes. Benign neighbor reads still work. This tests an
  explicitly modeled original-three-ID policy, not production tool authority.
- `STOCK_ALIAS_SQLITE_REJECTED`: report exact SQLite phase/code and absence of the
  payload from observed alias WAL. This is only this operation's refusal, not
  proof all alternate files or SQLite operations are confidential. If a failed
  SQL operation nevertheless emitted payload into WAL, the data-path test above
  runs instead; a SQL error cannot hide observed sensitive bytes.
- `STOCK_ALIAS_NO_ALTERNATE_OBSERVED`: successful commit is visible to canonical
  owner and no alternate WAL/SHM remains at observation. This reports only those
  facts, not proof of canonicalization or absence of transient sidecars.

The strict gate requires three paired runs/passes, a package pass and exactly one
branch marker per run. No skip, child test event, printed PASS, duplicate or mixed
branch can satisfy it. No branch is private-VFS integration or security GREEN.

Files remain self-created and temporary. The symlink is not a hard link and does
not change main link count; this avoids incorrectly claiming that existing
single-link qualification necessarily covers every alternate pathname. Existing
retired aliases before protection baseline remain a separate question.
