import assert from 'node:assert/strict';
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';
export const required = Object.freeze(['TestOwnedFDReadPreservesOffset', 'TestOwnedFDCallerCloseAndPathReplacement', 'TestOwnedFDAdmissionRefusals', 'TestOwnedFDBoundsAndObservedMutation', 'TestOwnedFDCancellationAndCloseRace', 'TestOwnedFDReadAndCloseFailures']);
const pkg = 'agent/internal/nativefs/darwinprobe';
export function validate(text) {
  assert(typeof text === 'string' && text.endsWith('\n'), 'truncated/non-string stream');
  const counts = new Map(required.map(name => [name, {run:0, pass:0, active:false, witness:0}]));
  let completed = false;
  for (const raw of text.slice(0,-1).split('\n')) {
    const line = raw.endsWith('\r') ? raw.slice(0,-1) : raw;
    assert(line.length && Buffer.byteLength(line) <= 1024*1024, 'empty/oversize event');
    const e = JSON.parse(line);
    assert(e && !Array.isArray(e) && typeof e.Action === 'string', 'malformed event');
    assert(e.Action !== 'fail' && e.Action !== 'skip' && !e.Action.endsWith('-fail'), 'failed/skipped event');
    if (e.Action === 'build-output') {
      assert(typeof e.ImportPath === 'string' && e.ImportPath.length && typeof e.Output === 'string' && !Object.hasOwn(e,'Test'), 'malformed build output');
      continue; // Neither source names nor printed PASS count as TestEvents.
    }
    assert(typeof e.Package === 'string' && e.Package.length, 'missing package');
    assert(['start','run','pass','output','pause','cont'].includes(e.Action), 'unknown action');
    if (e.Action === 'output') assert(typeof e.Output === 'string', 'malformed output');
    if (e.Package !== pkg) continue;
    assert(!completed, 'event after package completion');
    if (!Object.hasOwn(e,'Test')) {
      assert(['start','pass','output'].includes(e.Action), 'test action without name');
      if (e.Action === 'pass') {
        for (const c of counts.values()) assert(c.run===3 && c.pass===3 && !c.active, 'incomplete contract');
        completed = true;
      }
      continue;
    }
    assert(typeof e.Test === 'string' && counts.has(e.Test), 'unmanifested test');
    const c = counts.get(e.Test);
    if (e.Action === 'run') {
      assert(!c.active && c.run===c.pass && c.run<3, 'invalid run');
      c.run++; c.active=true; c.witness=0;
    } else if (e.Action === 'pass') {
      assert(c.active && c.run===c.pass+1, 'unpaired pass');
      assert(c.witness===1, 'missing native APFS fixture witness');
      c.pass++; c.active=false;
    } else if (e.Action === 'pause' || e.Action === 'cont') {
      assert(c.active, 'inactive test continuation');
    } else {
      assert(e.Action==='output', 'invalid named test action');
      if(e.Output.includes('OWNED_FD_TRUSTED_FIXTURE')) {
        assert(c.active, 'inactive native fixture witness');
        const matches=e.Output.match(/OWNED_FD_TRUSTED_FIXTURE APFS readonly regular links=1 bytes=[0-9]+(?=\r?\n|$)/g)??[];
        assert(matches.length===1 && (e.Output.match(/OWNED_FD_TRUSTED_FIXTURE/g)??[]).length===1, 'malformed fixture witness');
        assert(++c.witness===1, 'duplicate native fixture witness');
      }
    }
  }
  assert(completed, 'missing package completion');
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  validate(fs.readFileSync(process.argv[2], 'utf8'));
  console.log(`${required.length} exact trusted owned-FD contracts passed three native rounds. Not pathname reads, historical-copy isolation or production qualification.`);
}
