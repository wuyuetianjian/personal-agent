import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile,mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {PIN,validateInputs as validateAllInputs,testArguments,runExperiment,verifySources,createEventValidator} from './sqlite-private-sql-session-runner.mjs';
const mod=await readFile(new URL('../go.mod',import.meta.url),'utf8');
const sum=await readFile(new URL('../go.sum',import.meta.url),'utf8');
const basePatch=await readFile(new URL('./sqlite-persistent-vfs/lib_patch_windows.go.txt',import.meta.url),'utf8');
const mainPatch=await readFile(new URL('./sqlite-persistent-vfs/main_read_bridge_windows.go.txt',import.meta.url),'utf8');
const patch=await readFile(new URL('./sqlite-persistent-vfs/private_sql_session_windows.go.txt',import.meta.url),'utf8');
const walPatch=await readFile(new URL('./sqlite-persistent-vfs/existing_wal_bridge_windows.go.txt',import.meta.url),'utf8');
const accessPatch=await readFile(new URL('./sqlite-persistent-vfs/access_bridge_windows.go.txt',import.meta.url),'utf8');
const admissionPatch=await readFile(new URL('./sqlite-persistent-vfs/private_sql_admission_windows.go.txt',import.meta.url),'utf8');
const resourcePatch=await readFile(new URL('./sqlite-persistent-vfs/private_shm_resources_windows.go.txt',import.meta.url),'utf8');
const slotPatch=await readFile(new URL('./sqlite-persistent-vfs/private_shm_slots_windows.go.txt',import.meta.url),'utf8');
const diagnostics=await readFile(new URL('./sqlite-persistent-vfs/private_sql_diagnostics_windows.go.txt',import.meta.url),'utf8');
const driver=await readFile(new URL('./sqlite-persistent-vfs/private_sql_driver_diagnostics_windows.go.txt',import.meta.url),'utf8');
const validateInputs=(m,s,p,b,main)=>validateAllInputs(m,s,p,b,main,walPatch,accessPatch,admissionPatch,resourcePatch,slotPatch,diagnostics,driver);
test('exact official version, checksums and isolated patch tag required',()=>{
 validateInputs(mod,sum,patch,basePatch,mainPatch);
 assert.throws(()=>validateInputs(mod.replace(PIN.version,'v1.57.0'),sum,patch,basePatch,mainPatch));
 assert.throws(()=>validateInputs(mod,sum.replace(PIN.sum,'h1:wrong'),patch,basePatch,mainPatch));
 assert.throws(()=>validateInputs(mod+'\nreplace x => y\n',sum,patch,basePatch,mainPatch));
 assert.throws(()=>validateInputs(mod,sum,patch.replace('nativeprobe_sqlite_persistent','other'),basePatch,mainPatch));
});
test('native-only runner refuses Linux rather than skips',async()=>{await assert.rejects(runExperiment({platform:'linux'}),/Windows-only/)});
test('positive SQL session contract is repeated and cannot match fixture child',()=>{
 const a=testArguments('/tmp/x.mod');assert(a.includes('-count=3'));assert(a.includes('-tags=nativeprobe_sqlite_identity,nativeprobe_sqlite_lease,nativeprobe_sqlite_permit,nativeprobe_sqlite_persistent,nativeprobe_sqlite_main_read,nativeprobe_sqlite_existing_wal,nativeprobe_sqlite_access,nativeprobe_sqlite_private_shm,nativeprobe_sqlite_shm_resources,nativeprobe_sqlite_shm_slots,nativeprobe_sqlite_private_sql'));
 assert(!a.find(x=>x.startsWith('-run=')).includes('StockChild'));
});
test('changed official source cannot enter overlay',async()=>{
 const dir=await mkdtemp(path.join(os.tmpdir(),'persistent-pin-test-'));
 try {await writeFile(path.join(dir,'go.mod'),'modified');await assert.rejects(verifySources(dir),/SHA mismatch/)}finally{await rm(dir,{recursive:true})}
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
 const rel='scripts/sqlite-persistent-vfs/private_sql_session_windows.go.txt';
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
const pkg='agent/internal/nativefs/windowsprobe', name='TestPrivateSQLHeldWALRead';
const line=e=>Buffer.from(JSON.stringify(e)+'\n');
function complete(v, packagePass=true) {
 v.feed(line({Action:'start',Package:pkg}));
 for(let i=0;i<3;i++) for(const Test of ['TestPrivateSQLHeldWALRead', 'TestPrivateSQLNativeWALIndexWitness', 'TestPrivateSQLRowsLifetime', 'TestPrivateSQLStatementDrain', 'TestPrivateSQLReadonlyFailures', 'TestPrivateSQLStorageRequalification']) {v.feed(line({Action:'run',Package:pkg,Test}));v.feed(line({Action:'pass',Package:pkg,Test}));}
 if(packagePass) v.feed(line({Action:'pass',Package:pkg}));
}
test('three paired SQL session rounds and one package pass required',()=>{
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
test('valid build-output cannot satisfy existing WAL test and truncated streams refuse',()=>{
 const v=createEventValidator();v.feed(line({Action:'build-output',ImportPath:pkg,Output:'PASS'}));assert.throws(()=>v.finish());
 const partial=createEventValidator();partial.feed(Buffer.from('{"Action":"pass"}'));assert.throws(()=>partial.finish());
 const completeRun=createEventValidator();complete(completeRun);assert.throws(()=>completeRun.feed(line({Action:'output',Package:pkg,Output:'late'})));
});
test('linked output must match reviewed hash, mode and Windows-only package',async()=>{
 const {createHash}=await import('node:crypto');
 const {validateGenerated}=await import('./sqlite-private-sql-session-runner.mjs');
 const bytes=Buffer.from('//go:build windows && (amd64 || arm64) && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read\n\npackage sqlite3\n');
 const make=b=>({Version:'modernc.org/sqlite@v1.58.0-linked-main-v1',OutputSHA256:createHash('sha256').update(b).digest('hex')});
 validateGenerated(bytes,make(bytes));
 assert.throws(()=>validateGenerated(Buffer.concat([bytes,Buffer.from('x')]),make(bytes)));
 assert.throws(()=>validateGenerated(bytes,{...make(bytes),Version:'unreviewed'}));
 const wrong=Buffer.from('package sqlite3\n');assert.throws(()=>validateGenerated(wrong,make(wrong)));
 assert.throws(()=>validateGenerated(bytes,{Version:make(bytes).Version,OutputSHA256:'bad'}));
});

test('main overlay remains independently pinned',()=>assert.throws(()=>validateInputs(mod,sum,patch,basePatch,mainPatch+'\n'),/patch SHA mismatch/));
test('SQL session manifest is exactly six contracts without fixture children',async()=>{
 const {EXPECTED_TESTS}=await import('./sqlite-private-sql-session-runner.mjs');
 assert.deepEqual(EXPECTED_TESTS[pkg],['TestPrivateSQLHeldWALRead', 'TestPrivateSQLNativeWALIndexWitness', 'TestPrivateSQLRowsLifetime', 'TestPrivateSQLStatementDrain', 'TestPrivateSQLReadonlyFailures', 'TestPrivateSQLStorageRequalification']);
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

test('ambient fixture marker cannot divert the native parent into child-only setup',async()=>{
 const key='AGENT_PRIVATE_EXISTING_WAL_FIXTURE', saved=process.env[key];
 process.env[key]='1';
 let inspected=false;
 try {
  await assert.rejects(runExperiment({platform:'win32',log:()=>{},command:async(args,options)=>{
   assert.equal(Object.hasOwn(options.env,key),false,'ambient fixture route leaked into parent command');
   inspected=true;
   throw new Error('stop after environment inspection');
  }}),/stop after environment inspection/);
  assert.equal(inspected,true);
 } finally { if(saved===undefined)delete process.env[key];else process.env[key]=saved; }
});

test('WAL initializer output has its own reviewed mode, SHA and exact build boundary',async()=>{
 const {createHash}=await import('node:crypto');
 const {validateWALGenerated:validate}=await import('./sqlite-private-sql-session-runner.mjs');
 const main={OutputSHA256:'a'.repeat(64)};
 const validateWALGenerated=(b,m)=>validate(b,m,main);
 const bytes=Buffer.from('//go:build windows && (amd64 || arm64) && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal\n\npackage sqlite3\n');
 const make=b=>({Version:'modernc.org/sqlite@v1.58.0-linked-wal-v1',OutputSHA256:createHash('sha256').update(b).digest('hex'),WAL:{LinkedOutputSHA256:main.OutputSHA256}});
 validateWALGenerated(bytes,make(bytes));
 assert.throws(()=>validateWALGenerated(Buffer.concat([bytes,Buffer.from('x')]),make(bytes)));
 assert.throws(()=>validateWALGenerated(bytes,{...make(bytes),Version:'modernc.org/sqlite@v1.58.0-linked-main-v1'}));
 for(const body of ['package sqlite3\n',bytes.toString().replace(' && nativeprobe_sqlite_existing_wal',''),bytes.toString()+'//go:build windows\n']) {
  const wrong=Buffer.from(body);assert.throws(()=>validateWALGenerated(wrong,make(wrong)));
 }
 assert.throws(()=>validateWALGenerated(bytes,{Version:make(bytes).Version,OutputSHA256:'bad'}));
});
test('generated output installs exclusively and never replaces either earlier output',async()=>{
 const {installGeneratedOutput}=await import('./sqlite-private-sql-session-runner.mjs');
 const dir=await mkdtemp(path.join(os.tmpdir(),'wal-generated-exclusive-'));
 try {
  const main=path.join(dir,'main.go'),wal=path.join(dir,'wal.go');
  await installGeneratedOutput(main,Buffer.from('main'));
  await installGeneratedOutput(wal,Buffer.from('wal'));
  await assert.rejects(installGeneratedOutput(main,Buffer.from('wal overwrite')),/EEXIST/);
  await assert.rejects(installGeneratedOutput(wal,Buffer.from('second wal')),/EEXIST/);
  assert.equal(await readFile(main,'utf8'),'main');assert.equal(await readFile(wal,'utf8'),'wal');
 }finally{await rm(dir,{recursive:true,force:true})}
});


test('WAL output cannot link against a different main helper artifact',async()=>{
 const {createHash}=await import('node:crypto');
 const {validateWALGenerated}=await import('./sqlite-private-sql-session-runner.mjs');
 const bytes=Buffer.from('//go:build windows && (amd64 || arm64) && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal\n\npackage sqlite3\n');
 const manifest={Version:'modernc.org/sqlite@v1.58.0-linked-wal-v1',OutputSHA256:createHash('sha256').update(bytes).digest('hex'),WAL:{LinkedOutputSHA256:'a'.repeat(64)}};
 const main={OutputSHA256:'a'.repeat(64)};
 validateWALGenerated(bytes,manifest,main);
 assert.throws(()=>validateWALGenerated(bytes,manifest,{OutputSHA256:'b'.repeat(64)}));
 assert.throws(()=>validateWALGenerated(bytes,{...manifest,WAL:undefined},main));
});


test('WAL and access overlays keep independent pins in the composed admission',()=>{
 assert.throws(()=>validateAllInputs(mod,sum,patch,basePatch,mainPatch,walPatch+'\n',accessPatch,admissionPatch,resourcePatch,slotPatch,diagnostics,driver),/patch SHA mismatch/);
 assert.throws(()=>validateAllInputs(mod,sum,patch,basePatch,mainPatch,walPatch,accessPatch+'\n',admissionPatch,resourcePatch,slotPatch,diagnostics,driver),/patch SHA mismatch/);
});


test('prior admission overlay cannot silently change with SQL sessions',()=>{
 assert.throws(()=>validateAllInputs(mod,sum,patch,basePatch,mainPatch,walPatch,accessPatch,admissionPatch+'\n',resourcePatch,slotPatch,diagnostics,driver),/patch SHA mismatch/);
});

test('ambient retained-resource child marker cannot bypass parent contracts',async()=>{
 const key='AGENT_PRIVATE_SHM_RETAINED_FIXTURE', saved=process.env[key];
 process.env[key]='1';let inspected=false;
 try {
  await assert.rejects(runExperiment({platform:'win32',log:()=>{},command:async(args,options)=>{
   assert.equal(Object.hasOwn(options.env,key),false,'retained child marker leaked');inspected=true;
   throw new Error('stop after retained environment inspection');
  }}),/stop after retained environment inspection/);
  assert.equal(inspected,true);
 }finally{if(saved===undefined)delete process.env[key];else process.env[key]=saved;}
});

test('prior SHM resource overlay is independently pinned',()=>{
 assert.throws(()=>validateAllInputs(mod,sum,patch,basePatch,mainPatch,walPatch,accessPatch,admissionPatch,resourcePatch+'\n',slotPatch,diagnostics,driver),/patch SHA mismatch/);
});

test('ambient slot child marker cannot bypass parent contracts',async()=>{
 const key='AGENT_PRIVATE_SHM_SLOT_FIXTURE', saved=process.env[key];
 process.env[key]='1';let inspected=false;
 try {
  await assert.rejects(runExperiment({platform:'win32',log:()=>{},command:async(args,options)=>{
   assert.equal(Object.hasOwn(options.env,key),false,'retained child marker leaked');inspected=true;
   throw new Error('stop after retained environment inspection');
  }}),/stop after retained environment inspection/);
  assert.equal(inspected,true);
 }finally{if(saved===undefined)delete process.env[key];else process.env[key]=saved;}
});

test('slot and both diagnostic overlays independently pinned',()=>{
 for(const args of [[slotPatch+'\n',diagnostics,driver],[slotPatch,diagnostics+'\n',driver],[slotPatch,diagnostics,driver+'\n']]) assert.throws(()=>validateAllInputs(mod,sum,patch,basePatch,mainPatch,walPatch,accessPatch,admissionPatch,resourcePatch,...args),/patch SHA mismatch/);
});
test('all eleven diagnostic ABI source pins required',async()=>{
 const {SOURCE_SHA256}=await import('./sqlite-private-sql-session-runner.mjs');
 assert.equal(Object.keys(SOURCE_SHA256).length,11);
 for(const n of ['go.mod','conn.go','rows.go','stmt.go','lib/sqlite_g_000000000002c4cf.go','lib/sqlite_g_000000000003feef.go','lib/sqlite_g_000000000003fea8.go'])assert.match(SOURCE_SHA256[n],/^[a-f0-9]{64}$/);
});

test('both diagnostic overlays retain LF and driver package stays at module root',async()=>{
 const {execFile}=await import('node:child_process'); const {promisify}=await import('node:util'); const exec=promisify(execFile);
 const dir=await mkdtemp(path.join(os.tmpdir(),'sql-diagnostic-checkout-'));
 const git=(...args)=>exec('git',['-c','core.autocrlf=true','-c','core.safecrlf=false',...args],{cwd:dir});
 try{
  await git('init','-q');await writeFile(path.join(dir,'.gitattributes'),await readFile(new URL('../.gitattributes',import.meta.url)));
  for(const [name,bytes] of [['private_sql_diagnostics_windows.go.txt',diagnostics],['private_sql_driver_diagnostics_windows.go.txt',driver]]){
   const rel='scripts/sqlite-persistent-vfs/'+name;await mkdir(path.dirname(path.join(dir,rel)),{recursive:true});await writeFile(path.join(dir,rel),bytes);await git('add','--','.gitattributes',rel);await rm(path.join(dir,rel));await git('checkout-index','--force','--',rel);assert.equal(await readFile(path.join(dir,rel),'utf8'),bytes);
  }
  const runner=await readFile(new URL('./sqlite-private-sql-session-runner.mjs',import.meta.url),'utf8');
  assert.match(runner,/const DRIVER_NAME='nativeprobe_sqlite_private_sql_driver_diagnostics_windows.go'/);
  assert.match(runner,/writeFile\(path.join\(replacement,DRIVER_NAME\),driver,\{flag:'wx'\}\)/);
 }finally{await rm(dir,{recursive:true,force:true})}
});
