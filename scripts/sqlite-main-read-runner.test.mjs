import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {PIN,validateInputs,testArguments,runExperiment,verifySources,createEventValidator} from './sqlite-main-read-runner.mjs';
const mod=await readFile(new URL('../go.mod',import.meta.url),'utf8');
const sum=await readFile(new URL('../go.sum',import.meta.url),'utf8');
const basePatch=await readFile(new URL('./sqlite-persistent-vfs/lib_patch_windows.go.txt',import.meta.url),'utf8');
const patch=await readFile(new URL('./sqlite-persistent-vfs/main_read_bridge_windows.go.txt',import.meta.url),'utf8');
test('exact official version, checksums and isolated patch tag required',()=>{
 validateInputs(mod,sum,patch,basePatch);
 assert.throws(()=>validateInputs(mod.replace(PIN.version,'v1.57.0'),sum,patch,basePatch));
 assert.throws(()=>validateInputs(mod,sum.replace(PIN.sum,'h1:wrong'),patch,basePatch));
 assert.throws(()=>validateInputs(mod+'\nreplace x => y\n',sum,patch,basePatch));
 assert.throws(()=>validateInputs(mod,sum,patch.replace('nativeprobe_sqlite_persistent','other'),basePatch));
});
test('native-only runner refuses Linux rather than skips',async()=>{await assert.rejects(runExperiment({platform:'linux'}),/Windows-only/)});
test('positive main-method contract is repeated and cannot match fixture child',()=>{
 const a=testArguments('/tmp/x.mod');assert(a.includes('-count=3'));assert(a.includes('-tags=nativeprobe_sqlite_identity,nativeprobe_sqlite_lease,nativeprobe_sqlite_persistent,nativeprobe_sqlite_main_read'));
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
 const rel='scripts/sqlite-persistent-vfs/main_read_bridge_windows.go.txt';
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
  validateInputs(mod,sum,checkedOut,basePatch);
  assert.throws(()=>validateInputs(mod,sum,checkedOut.replaceAll('\n','\r\n'),basePatch),/patch SHA mismatch/);
 }finally{await rm(dir,{recursive:true,force:true})}
});
test('base overlay remains independently pinned',()=>{
 assert.throws(()=>validateInputs(mod,sum,patch,basePatch+'\n'),/patch SHA mismatch/);
});
const pkg='agent/internal/nativefs/windowsprobe', name='TestMainReadBridgePrivateMethods';
const line=e=>Buffer.from(JSON.stringify(e)+'\n');
function complete(v) {
 v.feed(line({Action:'start',Package:pkg}));
 for(let i=0;i<3;i++) for(const Test of [name,'TestMainReadBridgeLockTransitions']) {v.feed(line({Action:'run',Package:pkg,Test}));v.feed(line({Action:'pass',Package:pkg,Test}));}
 v.feed(line({Action:'pass',Package:pkg}));
}
test('three paired main-method rounds and one package pass required',()=>{
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
test('valid build-output cannot satisfy main method test and truncated streams refuse',()=>{
 const v=createEventValidator();v.feed(line({Action:'build-output',ImportPath:pkg,Output:'PASS'}));assert.throws(()=>v.finish());
 const partial=createEventValidator();partial.feed(Buffer.from('{"Action":"pass"}'));assert.throws(()=>partial.finish());
 const completeRun=createEventValidator();complete(completeRun);assert.throws(()=>completeRun.feed(line({Action:'output',Package:pkg,Output:'late'})));
});
