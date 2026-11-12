import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,readFile,writeFile,mkdir,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {EXPECTED_TESTS,testArguments,createEventValidator,runExperiment,verifySources} from './sqlite-wal-flags-runner.mjs';
const pkg='agent/internal/nativefs/windowsprobe';
const names=['TestStockWALFlagsReadonlyMain','TestStockWALFlagsReadWriteMain'];
const line=e=>Buffer.from(JSON.stringify(e)+'\n');
const events=()=>[{Action:'start',Package:pkg},...Array.from({length:3},()=>names.flatMap(Test=>[{Action:'run',Package:pkg,Test},{Action:'pass',Package:pkg,Test}])).flat(),{Action:'pass',Package:pkg}];
function feed(v,es){for(const e of es)v.feed(line(e));}
test('only two named diagnostic contracts, three rounds, independent tag',()=>{
 assert.deepEqual(EXPECTED_TESTS,{[pkg]:names});
 const args=testArguments('/tmp/check.mod');assert(args.includes('-count=3'));assert(args.includes('-tags=nativeprobe_sqlite_wal_flags'));assert(args.includes('./internal/nativefs/windowsprobe'));
 assert.equal(args.find(x=>x.startsWith('-run=')),'-run=^('+names.join('|')+')$');
});
test('native-only runner refuses Linux, never skips',async()=>assert.rejects(runExperiment({platform:'linux'}),/Windows-only/));
test('exact paired six events plus package completion pass',()=>{const v=createEventValidator();feed(v,events());v.finish();assert.throws(()=>v.finish());});
for(const Action of ['fail','skip','build-fail','link-fail'])test('reject '+Action+' including dependencies',()=>{
 const v=createEventValidator();assert.throws(()=>v.feed(line({Action,Package:'other',ImportPath:'other'})));
});
test('printed PASS, wrong package and build output cannot qualify',()=>{
 for(const es of [[{Action:'output',Package:pkg,Output:'PASS'}],events().map(e=>({...e,Package:'other'})),[{Action:'build-output',ImportPath:pkg,Output:'PASS'}]]){const v=createEventValidator();feed(v,es);assert.throws(()=>v.finish());}
});
test('legal Go1.26 build output accepted, malformed build output rejected',()=>{
 const v=createEventValidator();feed(v,[{Action:'build-output',ImportPath:'dependency',Output:'note'},...events()]);v.finish();
 for(const e of [{Action:'build-output',ImportPath:'' ,Output:'x'},{Action:'build-output',ImportPath:pkg,Output:'x',Test:names[0]},{Action:'build-output',ImportPath:pkg}]){const v=createEventValidator();assert.throws(()=>v.feed(line(e)));}
});
test('missing extra unpaired rounds and missing package completion rejected',()=>{
 const v=createEventValidator();assert.throws(()=>v.feed(line({Action:'pass',Package:pkg,Test:names[0]})));
 const w=createEventValidator();feed(w,events().slice(0,-1));assert.throws(()=>w.feed(line({Action:'run',Package:pkg,Test:names[0]})));assert.throws(()=>w.finish());
 const x=createEventValidator();assert.throws(()=>feed(x,events().filter(e=>e.Test!==names[1])));
});
test('unknown subtest, falsey Test field, named start and post-package output rejected',()=>{
 for(const Test of [null,false,0,'',names[0]+'/child','TestOther']){const v=createEventValidator();feed(v,events().slice(0,-1));assert.throws(()=>v.feed(line({Action:'pass',Package:pkg,Test})));}
 const v=createEventValidator();assert.throws(()=>v.feed(line({Action:'start',Package:pkg,Test:names[0]})));
 const w=createEventValidator();feed(w,events());assert.throws(()=>w.feed(line({Action:'output',Package:pkg,Output:'late'})));
});
test('chunked CRLF valid, malformed truncated oversized invalid',()=>{
 const b=Buffer.concat(events().map(line)).toString().replaceAll('\n','\r\n');const v=createEventValidator();for(let i=0;i<b.length;i+=5)v.feed(Buffer.from(b.slice(i,i+5)));v.finish();
 for(const raw of ['{\n','[]\n','\n','x'.repeat(1024*1024+1)]){const w=createEventValidator();assert.throws(()=>w.feed(Buffer.from(raw)));}
 const w=createEventValidator();w.feed(Buffer.from('{'));assert.throws(()=>w.finish());
});
test('changed official source cannot enter overlay',async()=>{
 const dir=await mkdtemp(path.join(os.tmpdir(),'wal-flags-source-'));try{await writeFile(path.join(dir,'sqlite.go'),'changed');await assert.rejects(verifySources(dir),/SHA mismatch/)}finally{await rm(dir,{recursive:true,force:true})}
});

test('official module and standalone diagnostic overlay pinned independently',async()=>{
 const {PIN,validateInputs}=await import('./sqlite-wal-flags-runner.mjs');
 const mod=await readFile(new URL('../go.mod',import.meta.url),'utf8'),sum=await readFile(new URL('../go.sum',import.meta.url),'utf8');
 const patch=await readFile(new URL('./sqlite-persistent-vfs/stock_wal_flags_windows.go.txt',import.meta.url),'utf8');
 validateInputs(mod,sum,patch);
 for(const [m,s,p] of [[mod.replace(PIN.version,'v1.57.0'),sum,patch],[mod,sum.replace(PIN.sum,'wrong'),patch],[mod+'\nreplace x => y\n',sum,patch],[mod,sum,patch+'\n'],[mod,sum,patch.replace('nativeprobe_sqlite_wal_flags','other')]])assert.throws(()=>validateInputs(m,s,p));
});
test('actual autocrlf Git checkout keeps diagnostic patch exact LF',async()=>{
 const {execFile}=await import('node:child_process'),{promisify}=await import('node:util');const exec=promisify(execFile);
 const {validateInputs}=await import('./sqlite-wal-flags-runner.mjs');
 const mod=await readFile(new URL('../go.mod',import.meta.url),'utf8'),sum=await readFile(new URL('../go.sum',import.meta.url),'utf8');
 const rel='scripts/sqlite-persistent-vfs/stock_wal_flags_windows.go.txt';const patch=await readFile(new URL('../'+rel,import.meta.url),'utf8');
 const dir=await mkdtemp(path.join(os.tmpdir(),'wal-flags-git-'));const git=(...a)=>exec('git',['-c','core.autocrlf=true','-c','core.safecrlf=false',...a],{cwd:dir});
 try{
  await git('init','-q');await writeFile(path.join(dir,'.gitattributes'),await readFile(new URL('../.gitattributes',import.meta.url)));
  await mkdir(path.dirname(path.join(dir,rel)),{recursive:true});await writeFile(path.join(dir,rel),patch);await git('add','--','.gitattributes',rel);await rm(path.join(dir,rel));await git('checkout-index','--force','--',rel);
  const actual=await readFile(path.join(dir,rel),'utf8');assert.equal(actual,patch);assert(!actual.includes('\r'));validateInputs(mod,sum,actual);
  assert.throws(()=>validateInputs(mod,sum,actual.replaceAll('\n','\r\n')),/patch SHA mismatch/);
 }finally{await rm(dir,{recursive:true,force:true})}
});

test('ambient child-routing marker cannot bypass parent diagnostics',async()=>{
 const key='AGENT_STOCK_WAL_FLAGS_FIXTURE',old=process.env[key];process.env[key]='ambient-untrusted-fixture';
 const stop=new Error('stop after options inspection');let inspected=false;
 try{
  await assert.rejects(runExperiment({platform:'win32',log:()=>{},command:async(args,options)=>{assert.equal(options.env[key],undefined);inspected=true;throw stop}}),e=>e===stop);
  assert(inspected);
 }finally{if(old===undefined)delete process.env[key];else process.env[key]=old}
});
