import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {PIN,validateInputs,testArguments,runExperiment,verifySources,createEventValidator} from './sqlite-access-runner.mjs';
const mod=await readFile(new URL('../go.mod',import.meta.url),'utf8');
const sum=await readFile(new URL('../go.sum',import.meta.url),'utf8');
const basePatch=await readFile(new URL('./sqlite-persistent-vfs/lib_patch_windows.go.txt',import.meta.url),'utf8');
const mainPatch=await readFile(new URL('./sqlite-persistent-vfs/main_read_bridge_windows.go.txt',import.meta.url),'utf8');
const patch=await readFile(new URL('./sqlite-persistent-vfs/access_bridge_windows.go.txt',import.meta.url),'utf8');
test('exact official version, checksums and isolated patch tag required',()=>{
 validateInputs(mod,sum,patch,basePatch,mainPatch);
 assert.throws(()=>validateInputs(mod.replace(PIN.version,'v1.57.0'),sum,patch,basePatch,mainPatch));
 assert.throws(()=>validateInputs(mod,sum.replace(PIN.sum,'h1:wrong'),patch,basePatch,mainPatch));
 assert.throws(()=>validateInputs(mod+'\nreplace x => y\n',sum,patch,basePatch,mainPatch));
 assert.throws(()=>validateInputs(mod,sum,patch.replace('nativeprobe_sqlite_persistent','other'),basePatch,mainPatch));
});
test('native-only runner refuses Linux rather than skips',async()=>{await assert.rejects(runExperiment({platform:'linux'}),/Windows-only/)});
test('positive access contract is repeated and cannot match fixture child',()=>{
 const a=testArguments('/tmp/x.mod');assert(a.includes('-count=3'));assert(a.includes('-tags=nativeprobe_sqlite_identity,nativeprobe_sqlite_lease,nativeprobe_sqlite_persistent,nativeprobe_sqlite_main_read,nativeprobe_sqlite_access'));
 assert(!a.find(x=>x.startsWith('-run=')).includes('StockChild'));
});
test('changed official source cannot enter overlay',async()=>{
 const dir=await mkdtemp(path.join(os.tmpdir(),'persistent-pin-test-'));
 try {await writeFile(path.join(dir,'sqlite.go'),'modified');await assert.rejects(verifySources(dir),/SHA mismatch/)}finally{await rm(dir,{recursive:true})}
});
test('RED, skip, missing or printed fake PASS cannot become success',()=>{
 for(const action of ['fail','skip']){const v=createEventValidator();assert.throws(()=>v.feed(Buffer.from(JSON.stringify({Action:action,Package:'agent/internal/nativefs/windowsprobe'})+'\n')))}
 const v=createEventValidator();v.feed(Buffer.from(JSON.stringify({Action:'output',Package:'agent/internal/nativefs/windowsprobe',Output:'PASS'})+'\n'));assert.throws(()=>v.finish());
});
test('actual Git checkout with autocrlf=true retains pinned LF patch bytes',async()=>{
 const {execFile}=await import('node:child_process');
 const {promisify}=await import('node:util');
 const exec=promisify(execFile);
 const dir=await mkdtemp(path.join(os.tmpdir(),'persistent-checkout-'));
 const rel='scripts/sqlite-persistent-vfs/access_bridge_windows.go.txt';
 const git=(...args)=>exec('git',['-c','core.autocrlf=true','-c','core.safecrlf=false',...args],{cwd:dir});
 try {
  await git('init','-q');
  await writeFile(path.join(dir,'.gitattributes'),await readFile(new URL('../.gitattributes',import.meta.url)));
  await mkdir(path.dirname(path.join(dir,rel)),{recursive:true});
  await writeFile(path.join(dir,rel),patch);
  await git('add','--','.gitattributes',rel);
  await rm(path.join(dir,rel));
  await git('checkout-index','--force','--',rel);
  const checkedOut=await readFile(path.join(dir,rel),'utf8');
  assert(!checkedOut.includes('\r'),'checkout converted the pinned patch to CRLF');
  assert.equal(checkedOut,patch);
  validateInputs(mod,sum,checkedOut,basePatch,mainPatch);
  assert.throws(()=>validateInputs(mod,sum,checkedOut.replaceAll('\n','\r\n'),basePatch,mainPatch),/patch SHA mismatch/);
 }finally{await rm(dir,{recursive:true,force:true})}
});
test('base overlay remains independently pinned',()=>{
 assert.throws(()=>validateInputs(mod,sum,patch,basePatch+'\n',mainPatch),/patch SHA mismatch/);
});
const pkg='agent/internal/nativefs/windowsprobe', name='TestPersistentAccessExistingWALAndJournalTruth';
const line=e=>Buffer.from(JSON.stringify(e)+'\n');
function complete(v, packagePass=true) {
 v.feed(line({Action:'start',Package:pkg}));
 for(let i=0;i<3;i++) for(const Test of ['TestPersistentAccessExistingWALAndJournalTruth', 'TestPersistentAccessMissingAndEmptySemantics', 'TestPersistentAccessRefusesUnqualifiedObservation', 'TestPersistentAccessObservationGrantsNoOpenAuthority']) {v.feed(line({Action:'run',Package:pkg,Test}));v.feed(line({Action:'pass',Package:pkg,Test}));}
 if(packagePass) v.feed(line({Action:'pass',Package:pkg}));
}
test('three paired access rounds and one package pass required',()=>{
 const v=createEventValidator();complete(v);v.finish();assert.throws(()=>v.finish());
 const wrong=createEventValidator();assert.throws(()=>wrong.feed(line({Action:'pass',Package:pkg,Test:name})));
 const missing=createEventValidator();assert.throws(()=>missing.feed(line({Action:'pass',Package:pkg})));
});
test('unexpected tests/actions, malformed build/output and inactive continuation refuse',()=>{
 for(const e of [
  {Action:'run',Package:pkg,Test:'TestUnexpected'},
  {Action:'build-fail',ImportPath:pkg},
  {Action:'wat',Package:pkg},
  {Action:'build-output',ImportPath:'',Output:'x'},
  {Action:'build-output',ImportPath:pkg,Output:'x',Test:name},
  {Action:'output',Package:pkg},
  {Action:'pause',Package:pkg,Test:name},
 ]) {const v=createEventValidator();assert.throws(()=>v.feed(line(e)),JSON.stringify(e));}
});
test('valid build-output cannot satisfy access test and truncated streams refuse',()=>{
 const v=createEventValidator();v.feed(line({Action:'build-output',ImportPath:pkg,Output:'PASS'}));assert.throws(()=>v.finish());
 const partial=createEventValidator();partial.feed(Buffer.from('{"Action":"pass"}'));assert.throws(()=>partial.finish());
 const completeRun=createEventValidator();complete(completeRun);assert.throws(()=>completeRun.feed(line({Action:'output',Package:pkg,Output:'late'})));
});
test('linked output must match reviewed hash, mode and Windows-only package',async()=>{
 const {createHash}=await import('node:crypto');
 const {validateGenerated}=await import('./sqlite-access-runner.mjs');
 const bytes=Buffer.from('//go:build windows && (amd64 || arm64) && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read\n\npackage sqlite3\n');
 const make=b=>({Version:'modernc.org/sqlite@v1.58.0-linked-main-v1',OutputSHA256:createHash('sha256').update(b).digest('hex')});
 validateGenerated(bytes,make(bytes));
 assert.throws(()=>validateGenerated(Buffer.concat([bytes,Buffer.from('x')]),make(bytes)));
 assert.throws(()=>validateGenerated(bytes,{...make(bytes),Version:'unreviewed'}));
 const wrong=Buffer.from('package sqlite3\n');assert.throws(()=>validateGenerated(wrong,make(wrong)));
 assert.throws(()=>validateGenerated(bytes,{Version:make(bytes).Version,OutputSHA256:'bad'}));
});

test('main overlay remains independently pinned',()=>assert.throws(()=>validateInputs(mod,sum,patch,basePatch,mainPatch+'\n'),/patch SHA mismatch/));
test('access manifest is exactly four contracts without fixture children',async()=>{
 const {EXPECTED_TESTS}=await import('./sqlite-access-runner.mjs');
 assert.deepEqual(EXPECTED_TESTS[pkg],['TestPersistentAccessExistingWALAndJournalTruth', 'TestPersistentAccessMissingAndEmptySemantics', 'TestPersistentAccessRefusesUnqualifiedObservation', 'TestPersistentAccessObservationGrantsNoOpenAuthority']);
});
test('falsey Test cannot impersonate package completion and named start refuses',()=>{
 for(const Test of ['',null,false,0]){const v=createEventValidator();complete(v,false);assert.throws(()=>v.feed(line({Action:'pass',Package:pkg,Test})));}
 const v=createEventValidator();assert.throws(()=>v.feed(line({Action:'start',Package:pkg,Test:name})));
});

test('fourth round and skipped dependency reject even after valid contracts',()=>{
 const v=createEventValidator();complete(v,false);assert.throws(()=>v.feed(line({Action:'run',Package:pkg,Test:name})));
 const w=createEventValidator();assert.throws(()=>w.feed(line({Action:'skip',Package:'other',Test:'Other'})));
});
test('streaming chunks and CRLF preserve exact manifest; oversized line fails closed',()=>{
 const chunks=[];complete({feed:c=>chunks.push(c)});
 const bytes=Buffer.from(Buffer.concat(chunks).toString().replaceAll('\n','\r\n'));
 const v=createEventValidator();for(let i=0;i<bytes.length;i+=7)v.feed(bytes.subarray(i,i+7));v.finish();
 const huge=createEventValidator();assert.throws(()=>huge.feed(Buffer.from('x'.repeat(1024*1024+1))));
});
