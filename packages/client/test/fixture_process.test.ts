import test from 'node:test';
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {fixtureName,readFixtureInfo,stopFixture} from './fixture_process.ts';

test('fixture executable has a Windows extension',()=>{
 assert.equal(fixtureName('win32'),'fixture.exe');
 assert.equal(fixtureName('darwin'),'fixture');
});
test('fixture startup reports spawn failure rather than hanging',{timeout:5000},async()=>{
 const child=spawn('nonexistent-owned-fixture-938174',[],{stdio:'pipe'});
 try{await assert.rejects(readFixtureInfo(child,500),/ENOENT/)}finally{await stopFixture(child,100)}
});
test('fixture startup preserves refusal diagnostics',{timeout:5000},async()=>{
 const child=spawn(process.execPath,['-e','console.error("native adapter unavailable");process.exit(1)'],{stdio:'pipe'});
 try{await assert.rejects(readFixtureInfo(child,2000),/native adapter unavailable/)}finally{await stopFixture(child,100)}
});
test('fixture startup timeout and forced shutdown are bounded',{timeout:5000},async()=>{
 const child=spawn(process.execPath,['-e','process.on("SIGINT",()=>{});setInterval(()=>{},1000)'],{stdio:'pipe'});
 try{await assert.rejects(readFixtureInfo(child,200),/timeout/)}finally{await stopFixture(child,100)}
 assert.ok(child.exitCode!==null||child.signalCode!==null);
});
test('fixture startup reads exactly the first JSON line',{timeout:5000},async()=>{
 const child=spawn(process.execPath,['-e','console.log(JSON.stringify({url:"http://127.0.0.1:1"}));console.log("later output");setInterval(()=>{},1000)'],{stdio:'pipe'});
 try{assert.deepEqual(await readFixtureInfo(child,2000),{url:'http://127.0.0.1:1'})}finally{await stopFixture(child,100)}
});
