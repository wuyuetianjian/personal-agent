import test from 'node:test';
import assert from 'node:assert/strict';
import { validateBulkEvents, bulkRequiredTests } from './darwin-bulk-gate.mjs';
const pkg='agent/internal/nativefs/darwinprobe';
function good(){ return [...bulkRequiredTests.flatMap(Test=>Array.from({length:3},()=>[{Package:pkg,Test,Action:'run'},{Package:pkg,Test,Action:'pass'}]).flat()),{Package:pkg,Action:'pass'}]; }
test('exact native event manifest accepted',()=>validateBulkEvents(good()));
for(const Action of ['fail','build-fail','skip'])test(`reject ${Action}`,()=>assert.throws(()=>validateBulkEvents([...good(),{Package:pkg,Action}])));
test('missing test cannot pass via printed PASS',()=>{const es=good().filter(e=>e.Test!==bulkRequiredTests[0]);es.push({Package:pkg,Action:'output',Output:`--- PASS: ${bulkRequiredTests[0]}\n`});assert.throws(()=>validateBulkEvents(es));});
test('wrong package cannot satisfy manifest',()=>assert.throws(()=>validateBulkEvents(good().map(e=>({...e,Package:'other'})))));
test('duplicate, missing round, missing package finish rejected',()=>{const es=good();for(const bad of [[...es,es[0]],es.slice(1),es.slice(0,-1)])assert.throws(()=>validateBulkEvents(bad));});
test('non-array and malformed event rejected',()=>{for(const bad of [null,{},[null],[{}],[{Action:'pass'}]])assert.throws(()=>validateBulkEvents(bad));});
// Go 1.26 cmd/go/internal/load/printer.go BuildEvent uses ImportPath,
// not TestEvent.Package. The documented actions are build-output/build-fail.
test('Go 1.26 build-output accepted without contributing test passes',()=>{
  const output={Action:'build-output',ImportPath:'modernc.org/sqlite',Output:'compiler note\n'};
  validateBulkEvents([output,...good()]);
  assert.throws(()=>validateBulkEvents([output]));
  assert.throws(()=>validateBulkEvents([{...output,Output:`--- PASS: ${bulkRequiredTests[0]}\n`},...good().filter(e=>e.Test!==bulkRequiredTests[0])]));
});
test('build events malformed or failed remain refused',()=>{
  for(const e of [{Action:'build-output',ImportPath:'p'},{Action:'build-output',Output:'x'},...['build-fail','link-fail','future-fail'].map(Action=>({Action,ImportPath:'p',Output:'x'}))])assert.throws(()=>validateBulkEvents([e,...good()]));
});
test('undocumented potential build-start/build-done are not silently accepted',()=>{
  for(const Action of ['build-start','build-done'])assert.throws(()=>validateBulkEvents([{Action,ImportPath:'p'},...good()]));
});
test('test pass requires matching preceding run and no duplicate active run',()=>{
  const es=good();
  const swapped=[es[1],es[0],...es.slice(2)];
  const overlap=[es[0],es[2],es[1],...es.slice(3)];
  assert.throws(()=>validateBulkEvents(swapped));
  assert.throws(()=>validateBulkEvents(overlap));
});
test('package finish cannot precede any test event',()=>{
  const es=good();
  assert.throws(()=>validateBulkEvents([es.at(-1),...es.slice(0,-1)]));
  assert.throws(()=>validateBulkEvents([...es,{Package:pkg,Test:bulkRequiredTests[0],Action:'output',Output:'late'}]));
});
test('unfinished optional test prevents package pass',()=>assert.throws(()=>validateBulkEvents([{Package:pkg,Test:'TestExtra',Action:'run'},...good()])));
