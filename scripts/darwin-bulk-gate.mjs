import assert from 'node:assert/strict';
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';
export const bulkRequiredTests = ['TestBulkNativeNamesAndIndependentCursor','TestBulkNativeUnsafeEntriesNoFIFOReader','TestBulkNativeUnsafeEntriesNoFIFOReader/fifo','TestBulkNativeUnsafeEntriesNoFIFOReader/symlink','TestBulkNativeLimitNoPartial','TestBulkNativeClosedCancelled','TestBulkNativeDescriptorOwnership'];
export function validateBulkEvents(events) {
  assert(Array.isArray(events) && events.length > 0, 'missing event array');
  const pkg = 'agent/internal/nativefs/darwinprobe';
  const active = new Set();
  let finished = false;
  for (const e of events) {
    assert(e && typeof e === 'object' && typeof e.Action === 'string', 'malformed event');
    assert(e.Action !== 'fail' && !e.Action.endsWith('-fail') && e.Action !== 'skip', `native ${e.Action}`);
    // Go 1.26's only non-failing BuildEvent is build-output. Neither it nor
    // any printed PASS text is a TestEvent. Future build actions fail closed.
    if (e.Action === 'build-output') {
      assert(typeof e.ImportPath === 'string' && typeof e.Output === 'string' && !e.Test, 'malformed build output');
      continue;
    }
    assert(typeof e.Package === 'string', 'missing test package');
    assert(['start','run','pause','cont','pass','output','bench'].includes(e.Action), 'unknown event action');
    if (e.Package !== pkg) continue;
    assert(!finished, 'test event after package completion');
    if (e.Test) {
      assert(typeof e.Test === 'string', 'malformed test name');
      if (e.Action === 'run') {
        assert(!active.has(e.Test), 'duplicate active run');
        active.add(e.Test);
      } else if (e.Action === 'pass') {
        assert(active.delete(e.Test), 'pass without preceding run');
      } else if (['pause','cont'].includes(e.Action)) {
        assert(active.has(e.Test), 'inactive test continuation');
      }
    } else if (e.Action === 'pass') {
      assert.equal(active.size, 0, 'package ended with unfinished tests');
      finished = true;
    }
  }
  assert.equal(events.filter(e => e.Package === pkg && !e.Test && e.Action === 'pass').length, 1, 'package must finish exactly once');
  for (const Test of bulkRequiredTests) {
    for (const Action of ['run', 'pass']) assert.equal(events.filter(e => e.Package === pkg && e.Test === Test && e.Action === Action).length, 3, `${Test} must ${Action} three times`);
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const events = fs.readFileSync(process.argv[2], 'utf8').trim().split('\n').map(JSON.parse);
  validateBulkEvents(events);
  console.log('Five native APFS contracts and both unsafe-entry subtests passed three times; metadata experiment only.');
}
