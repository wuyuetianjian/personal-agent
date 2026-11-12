import test from 'node:test';
import assert from 'node:assert/strict';
import {required, validate} from './windows-lease-gate.mjs';
const Package = 'agent/internal/nativefs/windowsprobe';
const events = () => [{Action:'start',Package}, ...Array.from({length:3}, () => required.flatMap(Test => [{Action:'run',Package,Test},{Action:'pass',Package,Test}])).flat(), {Action:'pass',Package}];
const encode = xs => xs.map(x=>JSON.stringify(x)).join('\n')+'\n';
test('exact three-round source manifest succeeds',()=>validate(encode(events())));
for(const Action of ['fail','skip','build-fail','link-fail']) test('reject '+Action+' anywhere',()=>{
 const es=events(); es.splice(1,0,{Action,ImportPath:'other',Package:'other'});assert.throws(()=>validate(encode(es)));
});
test('missing contract cannot use printed PASS',()=>{
 const es=events().filter(e=>e.Test!==required[0]);es.splice(1,0,{Action:'output',Package,Output:'--- PASS: '+required[0]});assert.throws(()=>validate(encode(es)));
});
test('wrong package and build output cannot satisfy contracts',()=>{
 assert.throws(()=>validate(encode(events().map(e=>({...e,Package:'other'})))));
 assert.throws(()=>validate(encode([{Action:'build-output',ImportPath:Package,Output:'PASS'}, {Action:'pass',Package}])));
});
test('Go1.26 build output accepted but structurally checked',()=>{
 validate(encode([{Action:'build-output',ImportPath:'p',Output:'compiler note'},...events()]));
 for(const e of [{Action:'build-output',ImportPath:'p'}, {Action:'build-output',Output:'x'}, {Action:'build-output',ImportPath:'p',Output:'x',Test:required[0]}, {Action:'build-start',ImportPath:'p'}]) assert.throws(()=>validate(encode([e,...events()])));
});
test('missing or repeated round rejected',()=>{
 const a=events();a.splice(1,2);assert.throws(()=>validate(encode(a)));
 const b=events();b.splice(-1,0,{Action:'run',Package,Test:required[0]},{Action:'pass',Package,Test:required[0]});assert.throws(()=>validate(encode(b)));
});
test('run/pass ordering and overlapping run rejected',()=>{
 const a=events();[a[1],a[2]]=[a[2],a[1]];assert.throws(()=>validate(encode(a)));
 const b=events();b.splice(2,0,b[1]);assert.throws(()=>validate(encode(b)));
});
test('package completion exactly once after all tests',()=>{
 const a=events();a.pop();assert.throws(()=>validate(encode(a)));
 const b=events();assert.throws(()=>validate(encode([b.at(-1),...b.slice(0,-1)])));
 assert.throws(()=>validate(encode([...events(),{Action:'pass',Package}])));
 assert.throws(()=>validate(encode([...events(),{Action:'output',Package,Output:'late'}])));
});
test('unknown tests including unmanifested subtests cannot hide',()=>{
 for(const Test of ['TestOther',required[0]+'/unlisted']){const es=events();es.splice(1,0,{Action:'run',Package,Test},{Action:'pass',Package,Test});assert.throws(()=>validate(encode(es)));}
});
test('malformed and truncated streams fail closed',()=>{
 for(const input of ['',null,'{}\n','[]\n','null\n','{\n',encode(events()).trimEnd(),encode(events()).replace('\n','\n\n')]) assert.throws(()=>validate(input));
});
test('oversize event and unknown action rejected',()=>{
 assert.throws(()=>validate(encode([{Action:'output',Package,Output:'x'.repeat(1024*1024)},...events()])));
 assert.throws(()=>validate(encode([{Action:'future-action',Package},...events()])));
});
test('pause and continuation require an active manifest test',()=>{
 const es=events();es.splice(2,0,{Action:'pause',Package,Test:required[0]},{Action:'cont',Package,Test:required[0]});validate(encode(es));
 assert.throws(()=>validate(encode([{Action:'cont',Package,Test:required[0]},...events()])));
});
test('top-level pass cannot replace required lease subtest',()=>{
 for(const missing of required.filter(n=>n.includes('/'))) assert.throws(()=>validate(encode(events().filter(e=>e.Test!==missing))));
});
test('CRLF stream preserves exact events',()=>validate(encode(events()).replaceAll('\n','\r\n')));
