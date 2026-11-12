import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {PIN,validateInputs,testArguments,runExperiment,verifySources,createEventValidator} from './sqlite-persistent-vfs-runner.mjs';
const mod=await readFile(new URL('../go.mod',import.meta.url),'utf8');
const sum=await readFile(new URL('../go.sum',import.meta.url),'utf8');
const patch=await readFile(new URL('./sqlite-persistent-vfs/lib_patch_windows.go.txt',import.meta.url),'utf8');
test('exact official version, checksums and isolated patch tag required',()=>{
 validateInputs(mod,sum,patch);
 assert.throws(()=>validateInputs(mod.replace(PIN.version,'v1.57.0'),sum,patch));
 assert.throws(()=>validateInputs(mod,sum.replace(PIN.sum,'h1:wrong'),patch));
 assert.throws(()=>validateInputs(mod+'\nreplace x => y\n',sum,patch));
 assert.throws(()=>validateInputs(mod,sum,patch.replace('nativeprobe_sqlite_persistent','other')));
});
test('native-only runner refuses Linux rather than skips',async()=>{await assert.rejects(runExperiment({platform:'linux'}),/Windows-only/)});
test('positive SQL contract is repeated and cannot match fixture child',()=>{
 const a=testArguments('/tmp/x.mod');assert(a.includes('-count=3'));assert(a.includes('-tags=nativeprobe_sqlite_persistent'));
 assert(!a.find(x=>x.startsWith('-run=')).includes('StockChild'));
});
test('changed official source cannot enter overlay',async()=>{
 const dir=await mkdtemp(path.join(os.tmpdir(),'persistent-pin-test-'));
 try {await writeFile(path.join(dir,'sqlite.go'),'modified');await assert.rejects(verifySources(dir),/SHA mismatch/)}finally{await rm(dir,{recursive:true})}
});
test('RED, skip, missing or printed fake PASS cannot become success',()=>{
 for(const action of ['fail','skip']){const v=createEventValidator();assert.throws(()=>v.feed(Buffer.from(JSON.stringify({Action:action,Package:'agent/internal/nativefs/rootlease'})+'\n')))}
 const v=createEventValidator();v.feed(Buffer.from(JSON.stringify({Action:'output',Package:'agent/internal/nativefs/rootlease',Output:'PASS'})+'\n'));assert.throws(()=>v.finish());
});
test('actual Git checkout with autocrlf=true retains pinned LF patch bytes',async()=>{
 const {execFile}=await import('node:child_process');
 const {promisify}=await import('node:util');
 const exec=promisify(execFile);
 const dir=await mkdtemp(path.join(os.tmpdir(),'persistent-checkout-'));
 const rel='scripts/sqlite-persistent-vfs/lib_patch_windows.go.txt';
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
  validateInputs(mod,sum,checkedOut);
  assert.throws(()=>validateInputs(mod,sum,checkedOut.replaceAll('\n','\r\n')),/patch SHA mismatch/);
 }finally{await rm(dir,{recursive:true,force:true})}
});
