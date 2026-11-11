import { EXPECTED_TESTS as OTHER_TESTS } from './sqlite-main-identity-runner.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,realpath,mkdir,readFile,writeFile,readdir,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {PIN,EXPECTED_TESTS,validateInputs,testArguments,createEventValidator,createOwnedFile,runExperiment} from './sqlite-temp-vfs-runner.mjs';
const mod='module agent\n\ngo 1.26.4\nrequire modernc.org/sqlite v1.58.0\n';
const sum='modernc.org/sqlite v1.58.0 h1:38u40/bwkfM7f0Myhosl+SEMltSDxnGdQf8o6Kjmys0=\nmodernc.org/sqlite v1.58.0/go.mod h1:rsD2CckafgObKC4DhBlGBf+RiHxkc3hINGt1Xw32tVY=\n';
function events(){const e=[];for(const [Package,names] of Object.entries(EXPECTED_TESTS)){for(let i=0;i<3;i++)for(const Test of names)e.push({Action:'run',Package,Test},{Action:'pass',Package,Test});e.push({Action:'pass',Package});}return e;}
function validate(e,sep='\n'){const v=createEventValidator();const data=Buffer.from(e.map(JSON.stringify).join(sep)+sep);for(let i=0;i<data.length;i+=7)v.feed(data.subarray(i,i+7));v.finish();}
test('stock pins forbid version, sum and replacement drift',()=>{
 assert.doesNotThrow(()=>validateInputs(mod,sum));
 for(const bad of [mod.replace('v1.58.0','v1.59.0'),mod+'replace modernc.org/sqlite => ../evil\n',mod+'replace (\n modernc.org/sqlite => ../evil\n)\n'])assert.throws(()=>validateInputs(bad,sum));
 assert.throws(()=>validateInputs(mod,sum.replace('38u40','wrong')));
});
test('manifest is exactly the seven TEMP tests with fixed command',()=>{
 assert.deepEqual(EXPECTED_TESTS,{'agent/internal/nativefs/rootlease':['TestNativeSQLiteTempSpillUsesOwnedScratch'],'agent/internal/nativefs/windowsprobe':['TestNativeSQLiteTempVFSRejectsPathsAndFlags','TestNativeSQLiteTempVFSRegistrationOwnership','TestNativeSQLiteTempScratchOffsetIO','TestNativeSQLiteTempScratchOwnership','TestNativeSQLiteTempScratchFailures','TestNativeSQLiteTempScratchParentMutation']});
 assert.deepEqual(testArguments('tmp.mod'),['test','-json','-modfile=tmp.mod','-tags=nativeprobe_sqlite_identity','-count=3','-timeout=10m','-run=^('+Object.values(EXPECTED_TESTS).flat().join('|')+')$','./internal/nativefs/rootlease','./internal/nativefs/windowsprobe']);
});
test('real events counted once with LF/CRLF and output ignored',()=>{for(const sep of ['\n','\r\n'])validate([{Action:'build-output',ImportPath:'a',Output:'build'},...events()],sep);});
test('zero missing skipped failed extra wrong-package and build-fail evidence refuse',()=>{
 const e=events();const bad=[[],e.filter(x=>x.Test!=='TestNativeSQLiteTempSpillUsesOwnedScratch'),e.slice(0,-1),e.map(x=>x.Test?{...x,Package:'wrong'}:x),[...e,{Action:'fail',Package:'other'}],[...e,{Action:'skip',Package:'other',Test:'x'}],[{Action:'build-fail',ImportPath:'bad'}],e.filter(x=>x.Action!=='run'),[...e,e[0]], [{Action:'output',Package:'agent/internal/nativefs/rootlease',Output:'--- PASS: TestNativeSQLiteTempSpillUsesOwnedScratch'}]];
 for(const b of bad)assert.throws(()=>validate(b));
 for(const name of Object.values(EXPECTED_TESTS).flat())for(const action of ['fail','skip'])assert.throws(()=>validate(e.map(x=>x.Test===name&&x.Action==='pass'?{...x,Action:action}:x)));
});
test('malformed truncated and over-bound event streams refuse',()=>{for(const input of ['not-json\n','{}\n','{"Action":"pass"}', 'x'.repeat(1024*1024+1)]){const v=createEventValidator();assert.throws(()=>{v.feed(Buffer.from(input));v.finish();});}});
test('non-Windows refuses rather than skips',async()=>{await assert.rejects(runExperiment({platform:'linux',command:()=>assert.fail('no command')}),/Windows-only/);});
async function fixture(t,mode='ok'){
 const root=await realpath(await mkdtemp(path.join(os.tmpdir(),'temp-vfs-runner-test-')));t.after(()=>rm(root,{recursive:true,force:true}));
 const repo=path.join(root,'repo'),cache=path.join(root,'cache'),source=path.join(cache,'modernc.org','sqlite@v1.58.0'),staging=path.join(root,'staging');
 await mkdir(repo);await mkdir(staging);await writeFile(path.join(repo,'go.mod'),mod);await writeFile(path.join(repo,'go.sum'),sum);
 const calls=[];let verifies=0;
 const command=async(args,options)=>{
  calls.push(args);assert.equal(options.cwd,repo);assert.equal(options.env.GOTOOLCHAIN,'local');assert.equal(options.env.GOWORK,'off');assert.equal(options.env.GOFLAGS,'');
  if(args[0]==='env')return JSON.stringify({GOMODCACHE:cache,GOVERSION:mode==='go-version'?'go1.26.3':'go1.26.4'});
  const modfile=args.find(a=>a.startsWith('-modfile='))?.slice(9);assert.equal(await readFile(modfile,'utf8'),mod,'stock modfile never receives a replace');
  if(args[0]==='mod'&&args[1]==='download'){
   await assert.rejects(realpath(cache),{code:'ENOENT'});await mkdir(source,{recursive:true});await writeFile(path.join(source,'go.mod'),'module modernc.org/sqlite\n');await writeFile(path.join(source,'sqlite.go'),'package sqlite\n');
   return JSON.stringify({Path:PIN.module,Version:mode==='download-version'?'v1.57.0':PIN.version,Sum:PIN.sum,GoModSum:PIN.goModSum,Dir:source});
  }
  if(args[0]==='list')return JSON.stringify({Path:PIN.module,Version:PIN.version,Dir:source,...(mode==='replace'?{Replace:{Path:'evil'}}:{})});
  if(args[0]==='mod'&&args[1]==='verify'){verifies++;if(mode==='post-verify'&&verifies===2)throw Error('post verification failed');return 'all modules verified\n';}
  if(args[0]==='test'){
   assert.equal(verifies,1);assert.deepEqual((await readdir(source)).sort(),['go.mod','sqlite.go']);
   if(mode==='test-fail')throw Error('real test exit 1');
   if(mode!=='zero')options.onStdout(Buffer.from(events().map(JSON.stringify).join('\n')+'\n'));
   return '';
  }throw Error('unexpected command');
 };
 if(mode==='ok')await runExperiment({platform:'win32',repo,tempRoot:staging,command,log:()=>{}});
 else await assert.rejects(runExperiment({platform:'win32',repo,tempRoot:staging,command,log:()=>{}}));
 assert.equal(await readFile(path.join(repo,'go.mod'),'utf8'),mod);assert.equal(await readFile(path.join(repo,'go.sum'),'utf8'),sum);assert.deepEqual(await readdir(staging),[]);
 if(['ok','test-fail','zero','post-verify'].includes(mode))assert.equal(verifies,2,'post verify required even when test/manifest fails');
 return calls;
}
test('cold official download, selected source verification, stock execution and cleanup',async t=>{const c=await fixture(t);assert.deepEqual(c.map(a=>a.slice(0,2)),[['env','-json'],['mod','download'],['list',c[2][1]],['mod','verify'],['test','-json'],['mod','verify']]);});
for(const mode of ['go-version','download-version','replace','test-fail','zero','post-verify'])test('fails closed: '+mode,t=>fixture(t,mode));
test('TEMP job is independent stock execution without continue-on-error and in package test',async()=>{
 const workflow=await readFile(new URL('../.github/workflows/native-conformance.yml',import.meta.url),'utf8');
 const job=workflow.split(/^  windows-sqlite-temp-vfs-evidence:\s*$/m)[1]?.split(/^  [a-z][a-z0-9-]*:\s*$/m)[0];
 assert.ok(job,'dedicated TEMP VFS job exists');assert.match(job,/runs-on: windows-2025/);assert.match(job,/go-version: '1\.26\.4'/);assert.match(job,/cache: false/);
 assert.match(job,/node --test scripts\/sqlite-temp-vfs-runner\.test\.mjs/);assert.match(job,/node scripts\/sqlite-temp-vfs-runner\.mjs/);assert.doesNotMatch(job,/continue-on-error|sqlite-main-identity-runner/);
 const pkg=JSON.parse(await readFile(new URL('../package.json',import.meta.url),'utf8'));assert.ok(pkg.scripts.test.includes('scripts/sqlite-temp-vfs-runner.test.mjs'));
});

test('exclusive modfile ownership never claims or removes an existing entry',async t=>{
 const dir=await realpath(await mkdtemp(path.join(os.tmpdir(),'temp-vfs-owner-')));t.after(()=>rm(dir,{recursive:true,force:true}));
 const file=path.join(dir,'experiment.mod');await writeFile(file,'unexpected');let owned=false;
 await assert.rejects(createOwnedFile(file,'replacement',()=>{owned=true;}),{code:'EEXIST'});
 assert.equal(owned,false);assert.equal(await readFile(file,'utf8'),'unexpected');
 const fresh=path.join(dir,'experiment.sum');await createOwnedFile(fresh,'sum',()=>{owned=true;});assert.equal(owned,true);assert.equal(await readFile(fresh,'utf8'),'sum');
});

for(const collision of ['experiment.mod','experiment.sum'])test('cleanup retains unowned '+collision+' EEXIST and reports incomplete cleanup',async t=>{
 const dir=await realpath(await mkdtemp(path.join(os.tmpdir(),'temp-vfs-collision-')));t.after(()=>rm(dir,{recursive:true,force:true}));
 const repo=path.join(dir,'repo'),staging=path.join(dir,'stage');await mkdir(repo);await mkdir(staging);await writeFile(path.join(repo,'go.mod'),mod);await writeFile(path.join(repo,'go.sum'),sum);
 let unexpected;
 const createFile=async(file,content,claim)=>{if(path.basename(file)===collision){unexpected=file;await writeFile(file,'not owned');}await createOwnedFile(file,content,claim);};
 await assert.rejects(runExperiment({platform:'win32',repo,tempRoot:staging,createFile,command:async args=>{
  if(args[0]==='env')return JSON.stringify({GOVERSION:'go1.26.4',GOMODCACHE:path.join(dir,'cache')});
  throw Error('download must not run after file collision');
 },log:()=>{}}),/cleanup incomplete/);
 assert.equal(await readFile(unexpected,'utf8'),'not owned');assert.deepEqual(await readdir(path.dirname(unexpected)),[collision]);
});
test('failure after claiming exclusive modfile still cleans owned entry',async t=>{
 const dir=await realpath(await mkdtemp(path.join(os.tmpdir(),'temp-vfs-write-fail-')));t.after(()=>rm(dir,{recursive:true,force:true}));
 const repo=path.join(dir,'repo'),staging=path.join(dir,'stage');await mkdir(repo);await mkdir(staging);await writeFile(path.join(repo,'go.mod'),mod);await writeFile(path.join(repo,'go.sum'),sum);
 await assert.rejects(runExperiment({platform:'win32',repo,tempRoot:staging,createFile:async(file,content,claim)=>{
  await createOwnedFile(file,content,claim);throw Error('controlled failure after exclusive ownership');
 },command:async args=>{assert.equal(args[0],'env');return JSON.stringify({GOVERSION:'go1.26.4',GOMODCACHE:path.join(dir,'cache')});},log:()=>{}}),/controlled failure/);
 assert.deepEqual(await readdir(staging),[]);
});

test('exact anchored command matches only this manifest, never the independent experiment',()=>{
 const names=Object.values(EXPECTED_TESTS).flat(),other=Object.values(OTHER_TESTS).flat();
 assert.equal(names.length,7);
 const text=testArguments('x.mod').find(a=>a.startsWith('-run=')).slice(5);
 assert.ok(text.startsWith('^(')&&text.endsWith(')$'));assert.ok(!text.includes('?!'));
 const regex=new RegExp(text);
 for(const name of names){assert.ok(regex.test(name));for(const extra of ['Before'+name,name+'After',name+'Extra'])assert.equal(regex.test(extra),false,extra);}
 for(const name of other)assert.equal(regex.test(name),false,name);
});

test('every new adapter contract must run/pass three times without missing or skipped evidence',()=>{
 const names=['TestNativeSQLiteTempScratchOffsetIO','TestNativeSQLiteTempScratchOwnership','TestNativeSQLiteTempScratchFailures','TestNativeSQLiteTempScratchParentMutation'];
 for(const name of names){
  assert.ok(EXPECTED_TESTS['agent/internal/nativefs/windowsprobe'].includes(name));
  assert.throws(()=>validate(events().filter(e=>e.Test!==name)));
  for(const action of ['fail','skip'])assert.throws(()=>validate(events().map(e=>e.Test===name&&e.Action==='pass'?{...e,Action:action}:e)));
 }
});
