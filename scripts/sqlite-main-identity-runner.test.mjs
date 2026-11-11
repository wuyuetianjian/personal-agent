import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, readdir, rm, symlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { validateInputs, copyModule, testArguments, runExperiment, PIN, PATCH_RELATIVE, createEventValidator, EXPECTED_TESTS } from './sqlite-main-identity-runner.mjs';

const mod = 'module agent\n\ngo 1.26.4\nrequire (\n modernc.org/sqlite v1.58.0\n)\n';
const sum = 'modernc.org/sqlite v1.58.0 h1:38u40/bwkfM7f0Myhosl+SEMltSDxnGdQf8o6Kjmys0=\nmodernc.org/sqlite v1.58.0/go.mod h1:rsD2CckafgObKC4DhBlGBf+RiHxkc3hINGt1Xw32tVY=\n';
const patch = '//go:build windows && nativeprobe_sqlite_identity\n\npackage sqlite\n';
async function fixture(t) { const dir = await mkdtemp(path.join(os.tmpdir(), 'sqlite-runner-test-')); t.after(() => rm(dir, {recursive:true,force:true})); return dir; }

test('fixed source pins and exact experimental patch gate are required', () => {
 assert.doesNotThrow(() => validateInputs(mod, sum, patch));
 assert.equal(PIN.version, 'v1.58.0');
 for (const bad of [mod.replace('v1.58.0', 'v1.59.0'), mod+'replace modernc.org/sqlite => ../other\n', mod+'replace (\n modernc.org/sqlite => ../other\n)\n']) assert.throws(() => validateInputs(bad, sum, patch));
 for (const bad of ['', sum.replace('38u40', 'wrong'), sum+sum.split('\n')[0]+'\n']) assert.throws(() => validateInputs(mod, bad, patch));
 for (const bad of [patch.replace('windows && ', ''), patch.replace('&&', '||'), patch.replace('package sqlite', 'package main')]) assert.throws(() => validateInputs(mod, sum, bad));
});

test('test command cannot omit isolation, tags, repetition or native packages', () => {
 assert.deepEqual(testArguments('/tmp/experiment.mod'), ['test','-json','-modfile=/tmp/experiment.mod','-tags=nativeprobe_sqlite_identity','-count=3','-timeout=10m','-run=^TestNative(Protected|SQLite)','./internal/nativefs/rootlease','./internal/nativefs/windowsprobe']);
});

test('module copy retains source/lib/licenses but excludes testdata and git at every level', async t => {
 const dir = await fixture(t); const source=path.join(dir,'source'), dest=path.join(dir,'copy');
 for (const name of ['lib','testdata','.git','lib/testdata']) await mkdir(path.join(source,name),{recursive:true});
 for (const name of ['go.mod','sqlite.go','LICENSE','lib/sqlite_windows_amd64.go','testdata/secret','.git/config','lib/testdata/secret']) await writeFile(path.join(source,name), name);
 await copyModule(source,dest);
 assert.deepEqual((await readdir(dest)).sort(), ['LICENSE','go.mod','lib','sqlite.go']);
 assert.deepEqual(await readdir(path.join(dest,'lib')),['sqlite_windows_amd64.go']);
 assert.equal(await readFile(path.join(source,'sqlite.go'),'utf8'),'sqlite.go');
});

test('module copy refuses symbolic links instead of following outside source', async t => {
 const dir=await fixture(t); const source=path.join(dir,'source'); await mkdir(source);
 await writeFile(path.join(dir,'outside'),'not module source'); await symlink(path.join(dir,'outside'),path.join(source,'link'));
 await assert.rejects(copyModule(source,path.join(dir,'copy')), /symlink|symbolic/i);
});

test('non-Windows invocation fails explicitly before executing commands', async () => {
 let called=false;
 await assert.rejects(runExperiment({platform:'linux', command:async()=>{called=true;}}), /Windows-only/);
 assert.equal(called,false);
});

async function stagedRun(t, {failTest=false, wrongDownload=false, noEvents=false}={}) {
 const dir=await fixture(t), repo=path.join(dir,'repo'), cache=path.join(dir,'cache'), module=path.join(cache,'modernc.org','sqlite@v1.58.0'), staging=path.join(dir,'staging');
 await mkdir(module,{recursive:true}); await mkdir(staging); await mkdir(path.join(repo,path.dirname(PATCH_RELATIVE)),{recursive:true});
 await writeFile(path.join(repo,'go.mod'),mod); await writeFile(path.join(repo,'go.sum'),sum); await writeFile(path.join(repo,PATCH_RELATIVE),patch);
 await writeFile(path.join(module,'go.mod'),'module modernc.org/sqlite\n'); await writeFile(path.join(module,'LICENSE'),'official fixture license'); await writeFile(path.join(module,'sqlite.go'),'package sqlite\n'); await mkdir(path.join(module,'lib')); await writeFile(path.join(module,'lib','source.go'),'package sqlite3\n');
 const calls=[], logs=[];
 const command=async(args, options)=>{
  calls.push(args);
  assert.equal(options.cwd,repo); assert.equal(options.env.GOWORK,'off'); assert.equal(options.env.GOTOOLCHAIN,'local'); assert.equal(options.env.GOFLAGS,'');
  if(args[0]==='env') return JSON.stringify({GOMODCACHE:cache});
  if(args[0]==='mod'&&args[1]==='download') return JSON.stringify({Path:PIN.module,Version:wrongDownload?'v1.57.0':PIN.version,Sum:PIN.sum,GoModSum:PIN.goModSum,Dir:module});
  if(args[0]==='mod'&&args[1]==='verify') return 'all modules verified\n';
  if(args[0]==='list') return JSON.stringify({Path:PIN.module,Version:PIN.version});
  if(args[0]==='test') {
   const modfile=args.find(x=>x.startsWith('-modfile=')).slice(9);
   const temporary=await readFile(modfile,'utf8');
   assert.match(temporary,/replace modernc\.org\/sqlite => /);
   const replacement=JSON.parse(temporary.trim().split(' => ').at(-1));
   assert.equal(await readFile(path.join(replacement,'nativeprobe_sqlite_identity_windows.go'),'utf8'),patch);
   assert.equal(await readFile(path.join(module,'sqlite.go'),'utf8'),'package sqlite\n');
   assert.equal((await readdir(module)).includes('nativeprobe_sqlite_identity_windows.go'),false);
   if(failTest) throw new Error('native test failed');
   if (!noEvents) options.onStdout(Buffer.from(validEvents().map(JSON.stringify).join('\n')+'\n'));
   return '';
  }
  throw new Error('unexpected command '+args.join(' '));
 };
 const action=runExperiment({platform:'win32', repo, tempRoot:staging, command, log:line=>logs.push(line)});
 if(failTest||wrongDownload||noEvents) await assert.rejects(action,wrongDownload?/version|download/i:noEvents?/manifest|missing|pass/i:/native test failed/); else {
  await action;
  assert.ok(logs.some(s=>s.includes('modernc.org/sqlite v1.58.0')));
  assert.ok(logs.some(s=>/patch SHA256=[0-9a-f]{64}/.test(s)));
  assert.ok(logs.some(s=>s.includes('go test')&&s.includes('-run=^TestNative(Protected|SQLite)')));
 }
 assert.deepEqual(await readdir(staging),[],'temporary module and modfiles must be removed');
 assert.equal(await readFile(path.join(repo,'go.mod'),'utf8'),mod); assert.equal(await readFile(path.join(repo,'go.sum'),'utf8'),sum);
 return calls;
}

test('verified temporary module replacement is used and removed on success', async t => {
 const calls=await stagedRun(t);
 const testIndex=calls.findIndex(a=>a[0]==='test');
 assert.ok(calls.slice(0,testIndex).some(a=>a[0]==='mod'&&a[1]==='verify'));
 assert.equal(calls.filter(a=>a[0]==='test').length,1);
});
test('native failure remains failure and cleans temporary files',async t=>{await stagedRun(t,{failTest:true});});
test('download version drift refuses before running tests and cleans files',async t=>{
 const calls=await stagedRun(t,{wrongDownload:true}); assert.equal(calls.some(a=>a[0]==='test'),false);
});

function validEvents() {
 const events=[];
 for(const [Package,names] of Object.entries(EXPECTED_TESTS)) {
  events.push({Action:'start',Package});
  for(let round=0;round<3;round++) for(const Test of names) events.push({Action:'run',Package,Test},{Action:'output',Package,Test,Output:'a unicode note: 测试\n'},{Action:'pass',Package,Test});
  events.push({Action:'pass',Package});
 }
 return events;
}
function verifyEvents(events) { const v=createEventValidator(); v.feed(Buffer.from(events.map(JSON.stringify).join('\n')+'\n')); v.finish(); }
test('manifest accepts exactly three actual passes per required test across chunk boundaries',()=>{
 const data=Buffer.from(validEvents().map(JSON.stringify).join('\r\n')+'\r\n');
 const v=createEventValidator(); for(let i=0;i<data.length;i+=7) v.feed(data.subarray(i,i+7)); assert.doesNotThrow(()=>v.finish());
});
test('manifest rejects absent tests, wrong packages, fewer/extra repetitions and missing package completion',()=>{
 const events=validEvents();
 assert.throws(()=>verifyEvents([]));
 assert.throws(()=>verifyEvents(events.filter(e=>e.Test!==EXPECTED_TESTS['agent/internal/nativefs/rootlease'][0])));
 assert.throws(()=>verifyEvents(events.map(e=>({...e,Package:'wrong/package'}))));
 const index=events.findIndex(e=>e.Action==='pass'&&e.Test);
 assert.throws(()=>verifyEvents(events.filter((_,i)=>i!==index)));
 assert.throws(()=>verifyEvents([...events.slice(0,index),events[index],...events.slice(index)]));
 assert.throws(()=>verifyEvents(events.filter(e=>e.Test||e.Action!=='pass')));
});
test('manifest refuses every skip/fail, including nested tests, despite all required passes',()=>{
 for(const Action of ['skip','fail','build-fail']) {
  assert.throws(()=>verifyEvents([{Action,Package:'agent/internal/nativefs/rootlease',Test:'TestNativeSQLiteExtra/subcase'},...validEvents()]));
 }
});
test('printed PASS text, malformed JSON, truncated stream and oversized lines cannot satisfy manifest',()=>{
 assert.throws(()=>verifyEvents([{Action:'output',Package:'agent/internal/nativefs/rootlease',Output:'PASS all tests'}]));
 assert.throws(()=>{const v=createEventValidator();v.feed(Buffer.from('not JSON\n'));v.finish();});
 assert.throws(()=>{const v=createEventValidator();v.feed(Buffer.from('{"Action":'));v.finish();});
 assert.throws(()=>{const v=createEventValidator();v.feed(Buffer.from('x'.repeat(1024*1024+1)));v.finish();});
});
test('exit-zero command with no native events fails and still cleans temporary copy',async t=>{await stagedRun(t,{noEvents:true});});
test('Go 1.26 build-output uses ImportPath, contributes no passes, and build-fail still refuses',()=>{
 assert.doesNotThrow(()=>verifyEvents([{Action:'build-output',ImportPath:'modernc.org/sqlite',Output:'compiler note\n'},...validEvents()]));
 assert.throws(()=>verifyEvents([{Action:'build-fail',ImportPath:'modernc.org/sqlite'},...validEvents()]));
});
