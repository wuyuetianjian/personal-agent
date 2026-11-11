import assert from 'node:assert/strict';
import { readFileSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { test } from 'node:test';

const workflow = readFileSync(new URL('../.github/workflows/native-conformance.yml', import.meta.url), 'utf8');
function gate(name, workflowText = workflow) {
  const workflow = workflowText.replace(/\r\n/g, '\n');
  const start = workflow.indexOf(`      - name: ${name}\n`);
  assert(start >= 0, `missing workflow gate ${name}`);
  const end = workflow.indexOf('\n      - name:', start + 1);
  const block = workflow.slice(start, end < 0 ? undefined : end);
  const match = block.match(/node --input-type=module <<'JS'\n([\s\S]*?)\n          JS/);
  assert(match, `missing inline validator ${name}`);
  return match[1].split('\n').map(line => line.startsWith('          ') ? line.slice(10) : line).join('\n');
}
function names(list) { return [...list.matchAll(/'([^']+)'/g)].map(m => m[1]); }
function eventsFor(script, foundation) {
  const required = script.match(/const required = \{([\s\S]*?)\n\};/);
  assert(required, 'missing required-test manifest');
  const events = [];
  for (const m of required[1].matchAll(/(?:'([^']+)'|(\w+)):\s*\[([^\]]*)\]/g)) {
    const pkg = foundation ? `agent/internal/nativefs/${m[2]}` : m[1];
    assert(pkg);
    events.push({ Package: pkg, Action: 'pass' });
    for (const name of names(m[3])) events.push({ Package: pkg, Test: name, Action: 'run' }, { Package: pkg, Test: name, Action: 'pass' });
  }
  assert(events.length > 0);
  if (foundation) {
    const lease = script.match(/const leaseTests = \[([^\]]*)\]/);
    assert(lease);
    events.push({ Package: 'agent/internal/nativefs/rootlease', Action: 'pass' });
    for (const name of [...names(lease[1]), 'TestNativeRegistryCurrentTokenLifecycle']) {
      events.push({ Package: 'agent/internal/nativefs/rootlease', Test: name, Action: 'pass' });
    }
  }
  return events;
}
function runGate(script, filename, events) {
  const dir = mkdtempSync(join(tmpdir(), 'native-gate-'));
  try {
    writeFileSync(join(dir, filename), events.map(e => JSON.stringify(e)).join('\n'));
    const result = spawnSync(process.execPath, ['--input-type=module', '-e', script], {
      cwd: dir, env: { ...process.env, GITHUB_STEP_SUMMARY: join(dir, 'summary.txt') }, encoding: 'utf8', timeout: 10_000,
    });
    assert.ifError(result.error);
    return { status: result.status, detail: result.stderr + result.stdout };
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
const native = gate('Require all native contract tests without skips');
const foundation = gate('Require complete foundation tests without skips');

test('native refusal gate accepts its own packages without unrelated lease events', () => {
  const events = eventsFor(native, false);
  assert(events.every(e => ['agent/internal/interaction', 'agent/internal/api'].includes(e.Package)));
  const result = runGate(native, 'native-tests.json', events);
  assert.equal(result.status, 0, result.detail);
});
test('native refusal gate rejects missing required tests and skips', () => {
  const valid = eventsFor(native, false);
  const missing = valid.filter(e => e.Test !== 'TestNativeSupportDisabledTargets');
  assert.notEqual(runGate(native, 'native-tests.json', missing).status, 0);
  assert.notEqual(runGate(native, 'native-tests.json', [...valid, { Action: 'skip' }]).status, 0);
});
test('foundation gate requires its own lease events', () => {
  const events = eventsFor(foundation, true);
  const result = runGate(foundation, 'foundation-tests.json', events);
  assert.equal(result.status, 0, result.detail);
  assert.notEqual(runGate(foundation, 'foundation-tests.json', events.filter(e => e.Package !== 'agent/internal/nativefs/rootlease')).status, 0);
  assert.notEqual(runGate(foundation, 'foundation-tests.json', events.filter(e => !(e.Package === 'agent/internal/nativefs/rootlease' && !e.Test))).status, 0);
});
test('foundation gate rejects actual test failures and skips', () => {
  const events = eventsFor(foundation, true);
  for (const action of ['fail', 'skip']) assert.notEqual(runGate(foundation, 'foundation-tests.json', [...events, { Action: action }]).status, 0);
});

test('workflow extraction accepts LF and CRLF checkouts identically', () => {
  for (const name of ['Require all native contract tests without skips', 'Require complete foundation tests without skips']) {
    const lf = workflow.replace(/\r\n/g, '\n');
    assert.equal(gate(name, lf.replace(/\n/g, '\r\n')), gate(name, lf));
  }
});

const scratchTests = ['TestNativeStorageScratchLifecycle', 'TestNativeStorageScratchExistingCollisions', 'TestNativeStorageScratchMutatedPinnedParent'];
const windowsFoundation = foundation.replaceAll('process.platform', "'win32'");
const macFoundation = foundation.replaceAll('process.platform', "'darwin'");

test('Windows foundation requires every scratch test exactly once, with no missing/failed/skipped event', () => {
 for (const name of scratchTests) assert(foundation.includes("'"+name+"'"), `scratch test missing from foundation manifest: ${name}`);
 const valid=eventsFor(windowsFoundation,true);
 assert.equal(runGate(windowsFoundation,'foundation-tests.json',valid).status,0);
 for (const name of scratchTests) {
  assert.notEqual(runGate(windowsFoundation,'foundation-tests.json',valid.filter(e=>e.Test!==name)).status,0);
  for(const Action of ['skip','fail']) assert.notEqual(runGate(windowsFoundation,'foundation-tests.json',valid.map(e=>e.Test===name&&e.Action==='pass'?{...e,Action}:e)).status,0);
  assert.notEqual(runGate(windowsFoundation,'foundation-tests.json',[...valid,{Package:'agent/internal/nativefs/windowsprobe',Test:name,Action:'pass'}]).status,0);
  assert.notEqual(runGate(windowsFoundation,'foundation-tests.json',valid.filter(e=>!(e.Test===name&&e.Action==='run'))).status,0);
 }
});
test('macOS foundation does not require Windows scratch tests', () => {
 const events=eventsFor(macFoundation,true).filter(e=>!scratchTests.includes(e.Test));
 const result=runGate(macFoundation,'foundation-tests.json',events);
 assert.equal(result.status,0,result.detail);
});
function isolatedEvents() {
 const events=[];
 for(const Package of ['agent/internal/nativefs/windowsprobe','agent/internal/nativefs/rootlease']) events.push({Package,Action:'pass'});
 for(let i=0;i<3;i++)for(const Test of scratchTests)events.push({Package:'agent/internal/nativefs/windowsprobe',Test,Action:'run'},{Package:'agent/internal/nativefs/windowsprobe',Test,Action:'pass'});
 return events;
}
test('isolated scratch gate requires three real repetitions per test and both package completions',()=>{
 const script=gate('Require isolated scratch evidence without skips');
 const valid=isolatedEvents();
 assert.equal(runGate(script,'isolated-foundation-tests.json',valid).status,0);
 for(const name of scratchTests) {
  assert.notEqual(runGate(script,'isolated-foundation-tests.json',valid.filter(e=>e.Test!==name)).status,0);
  const missing=valid.findIndex(e=>e.Test===name&&e.Action==='pass');
  assert.notEqual(runGate(script,'isolated-foundation-tests.json',valid.filter((_,i)=>i!==missing)).status,0);
  assert.notEqual(runGate(script,'isolated-foundation-tests.json',[...valid,{Package:'agent/internal/nativefs/windowsprobe',Test:name,Action:'pass'}]).status,0);
  for(const Action of ['fail','skip']) assert.notEqual(runGate(script,'isolated-foundation-tests.json',[...valid,{Package:'agent/internal/nativefs/windowsprobe',Test:name,Action}]).status,0);
 }
 for(const pkg of ['agent/internal/nativefs/windowsprobe','agent/internal/nativefs/rootlease']) assert.notEqual(runGate(script,'isolated-foundation-tests.json',valid.filter(e=>e.Test||e.Package!==pkg)).status,0);
 assert.notEqual(runGate(script,'isolated-foundation-tests.json',[...valid,{Package:'agent/internal/nativefs/windowsprobe',Test:'TestNativeOpenLocalRootReducedToken',Action:'fail'}]).status,0);
 assert.notEqual(runGate(script,'isolated-foundation-tests.json',valid.map(e=>({...e,Package:'unrelated/package'}))).status,0);
});
test('isolated validator is LF/CRLF stable and consumes its own JSON test stream even after failure',()=>{
 const name='Require isolated scratch evidence without skips';
 assert.equal(gate(name,workflow.replace(/\r?\n/g,'\r\n')),gate(name));
 assert.match(workflow,/id: isolated_foundation/);
 assert.match(workflow,/go test -json -count=3 -timeout=5m .*windowsprobe .*rootlease \| tee isolated-foundation-tests\.json/);
 assert.match(workflow,/steps\.isolated_foundation\.outcome != 'skipped'/);
});
