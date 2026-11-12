import assert from 'node:assert/strict';
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';
import {required as lease} from './windows-lease-gate.mjs';
import {required as permit} from './windows-permit-gate.mjs';
export const required = Object.freeze([...lease,...permit]);
const pkg = 'agent/internal/nativefs/windowsprobe';
export function validate(text) {
  assert(typeof text === 'string' && text.endsWith('\n'), 'truncated/non-string stream');
  const counts = new Map(required.map(name => [name, {run:0, pass:0, active:false}]));
  let completed = false, tokenSeen = false, testSeen = false;
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
    if (e.Action==='output' && e.Output.startsWith('TOKEN_GUARD_OK ')) {
      assert(!Object.hasOwn(e,'Test') && !tokenSeen && !testSeen, 'duplicate/late/named token guard');
      validateToken(JSON.parse(e.Output.slice('TOKEN_GUARD_OK '.length).trim()));
      tokenSeen=true;
    }
    if (e.Action==='run') { assert(tokenSeen, 'test before qualified actual token'); testSeen=true; }

    if (!Object.hasOwn(e,'Test')) {
      assert(['start','pass','output'].includes(e.Action), 'test action without name');
      if (e.Action === 'pass') {
        assert(tokenSeen, 'missing actual child token evidence');
        for (const c of counts.values()) assert(c.run===3 && c.pass===3 && !c.active, 'incomplete contract');
        completed = true;
      }
      continue;
    }
    assert(typeof e.Test === 'string' && counts.has(e.Test), 'unmanifested test');
    const c = counts.get(e.Test);
    if (e.Action === 'run') {
      assert(!c.active && c.run===c.pass && c.run<3, 'invalid run');
      c.run++; c.active=true;
    } else if (e.Action === 'pass') {
      assert(c.active && c.run===c.pass+1, 'unpaired pass');
      c.pass++; c.active=false;
    } else if (e.Action === 'pause' || e.Action === 'cont') {
      assert(c.active, 'inactive test continuation');
    } else {
      assert(e.Action==='output', 'invalid named test action');
    }
  }
  assert(completed, 'missing package completion');
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  validate(fs.readFileSync(process.argv[2], 'utf8'));
  console.log(`${required.length} exact filtered-token contracts passed three native rounds. Not a separate standard-user account or production qualification.`);
}

export function validateToken(s) {
  assert(s && typeof s==='object' && !Array.isArray(s),'invalid token snapshot');
  assert.deepEqual(Object.keys(s).sort(), ['AdminAttributes','Elevation','ElevationType','EnabledPrivileges','Integrity'], 'unexpected token fields');
  assert(s.Elevation===0 && [1,3].includes(s.ElevationType),'elevated/full/unknown token');
  assert(s.Integrity==='S-1-16-8192','not exact Medium IL');
  assert(s.AdminAttributes===null || Array.isArray(s.AdminAttributes),'invalid admin attributes');
  for(const a of s.AdminAttributes??[]) assert(Number.isInteger(a)&&a>=0&&a<=0xffffffff&&(a&4)===0&&(a&16)!==0,'admin enabled/not deny-only');
  assert(s.EnabledPrivileges===null || Array.isArray(s.EnabledPrivileges),'invalid privileges');
  for(const p of s.EnabledPrivileges??[]) assert(p==='SeChangeNotifyPrivilege','unexpected enabled privilege');
}
