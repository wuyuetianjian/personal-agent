import assert from 'node:assert/strict';
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';

export const required = Object.freeze(['TestReadCleanupSuccessReturnsNoBytesOnCloseError', 'TestReadCleanupPreservesPrimaryFailure', 'TestReadCleanupOwnsMetadataHandleWithOpenError', 'TestGuardCleanupPreservesQualificationFailure', 'TestGuardCleanupOwnsHandleWithOpenError']);
const pkg = 'agent/internal/nativefs/windowsprobe';
export function validate(text) {
  assert(text.endsWith('\n'), 'truncated event stream');
  const counts = new Map(required.map(n => [n, { run: 0, pass: 0 }]));
  let completed = false;
  for (const line of text.trimEnd().split('\n')) {
    assert(Buffer.byteLength(line) <= 1024 * 1024, 'oversize event');
    const e = JSON.parse(line);
    assert(e && typeof e.Action === 'string', 'invalid action');
    assert(!['skip', 'fail'].includes(e.Action) && !e.Action.endsWith('-fail'), 'failure/skip event');
    if (e.Action === 'build-output' && typeof e.ImportPath === 'string') continue;
    assert(typeof e.Package === 'string', 'missing package');
    if (e.Package !== pkg) continue;
    assert(!completed, 'event after completion');
    if (!e.Test && e.Action === 'pass') {
      for (const c of counts.values()) assert.deepEqual(c, { run: 3, pass: 3 }, 'incomplete contract');
      completed = true;
      continue;
    }
    const c = counts.get(e.Test);
    if (!c) continue;
    if (e.Action === 'run') { assert(c.run === c.pass && c.run < 3, 'invalid run'); c.run++; }
    if (e.Action === 'pass') { assert(c.run === c.pass + 1, 'unpaired pass'); c.pass++; }
  }
  assert(completed, 'missing package completion');
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  validate(fs.readFileSync(process.argv[2], 'utf8'));
  console.log('All five temporary read-handle cleanup contracts passed three native rounds. Production remains disabled.');
}
