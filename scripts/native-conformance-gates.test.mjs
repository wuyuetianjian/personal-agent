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
    for (const name of names(m[3])) events.push({ Package: pkg, Test: name, Action: 'pass' });
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
