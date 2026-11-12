# Private readonly SQL: bounded stock interoperability validation

This adds two native validation contracts, not a new VFS capability. It uses the
existing fixed `SELECT value FROM fixture` session, B mapping/C locks and D bridge
unchanged. D first passed on 8ab8472, run 43260945404: 18 top-level passes and all
four retained child cases in each of three rounds. These interop tests are not
represented as passed until their own native evidence is collected.

Build tag: `nativeprobe_sqlite_private_sql_interop`, in addition to D's tags.
Exactly two top-level tests, each three times:

- `TestPrivateSQLStockAppendSnapshot`
- `TestPrivateSQLQuiescentCheckpointReopen`

No new lib/driver overlays or modified prior tests/runners are required.
`AGENT_PRIVATE_SQL_INTEROP_FIXTURE` is a dedicated stock-child routing marker;
the parent runner must clear it. Only each test explicitly sets it for a child.

## Independent stock process and protocol

The child uses SQLite's unchanged default VFS, one persistent writer connection,
WAL mode and disabled automatic checkpointing. It creates the schema, completes
a TRUNCATE checkpoint, then commits 128 distinct approximately 2 KiB text rows.
The WAL exceeds 256 KiB (multiple DB/WAL pages, not a claim of multiple SHM
index regions); an independent immutable main-only connection (fixture
control only) confirms that none of these rows exist in the checkpointed main.
The child begins and reads a real transaction, establishing a held readmark.

Bounded JSON-line replies report actual state for `ready`, `append`, `checkpoint`
and `stop`. `append` ends the previous stock read transaction without closing the
writer connection, appends 128 more full payload rows, and holds a new read
transaction at the new version. `checkpoint` ends its read transaction, runs real
`PRAGMA wal_checkpoint(TRUNCATE)` and reports busy/log/checkpointed plus count.
`stop` retires the stock transaction/connection/pool; parent verifies normal exit.
Timeout, pipe error, unexpected reply or nonzero child exit is failure, not a pass.
The process uses no private bridge for its stock controls.

## Active reader snapshot across append

The first test opens actual private Rows, consumes one row and observes the live
normal/reliable readonly index and real reader-slot witness. Stock then releases
its old reader and appends using its still-open writer connection. An independent
stock reader sees 256 complete payloads, while the original private Rows must
continue to return exactly the original 128 values. The test compares sets rather
than relying on unspecified SELECT row order, rejects duplicates, and traverses
all rows after append across pages. After EOF/Close, a new query in the same
private session must see all 256 rows. Private SQL and owner retire normally.

## Quiescent checkpoint and new owner

The second test completes private Rows and normal session teardown, including
Conn/DB, actual VFS file retirement, B/C attachments and owner. Only then does
stock end its reader and execute TRUNCATE. All three checkpoint counters must be
zero, WAL size must be zero, and independent stock reads must preserve all rows.
Stock appends another batch and establishes a readmark. The old private binding
must reject use; a newly qualified owner/session reads all 256 payloads and
retires normally. Same-file truncation is legal and does not require changed
numerical file IDs. This is not an active-owner sidecar deletion/rebinding test.

## Boundaries

No missing-readmark fabrication, raw SHM editing, automatic recovery, writable
private mappings, heap fallback allowance, journal-mode migration, stock-global
hooks, live sidecar replacement or tool-read enablement is added. These are two
bounded cooperating SQLite schedules, not all writer/checkpoint/rebuild races.
Unknown aliases, ordinary-user qualification, arbitrary SQL and in-flight SQL
cancellation remain outside the qualified scope.
