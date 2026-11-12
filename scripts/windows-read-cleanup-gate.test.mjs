import test from 'node:test';
import assert from 'node:assert/strict';
import { required, validate } from './windows-read-cleanup-gate.mjs';
const Package = 'agent/internal/nativefs/windowsprobe';
const events = () => [...Array.from({length: 3}, () => required.flatMap(Test => [{Action:'run',Package,Test},{Action:'pass',Package,Test}])).flat(), {Action:'pass',Package}];
const encode = a => a.map(e => JSON.stringify(e)).join('\n')+'\n';
test('accept exact three-run manifest', () => validate(encode(events())));
for (const action of ['fail','skip','build-fail']) test('reject '+action, () => {
 const e=events();e.splice(2,0,{Action:action,Package,Test:required[0]+'/child'});assert.throws(()=>validate(encode(e)));
});
test('printed PASS cannot substitute contract',()=>{const e=events();e[1]={Action:'output',Package,Output:'PASS '+required[0]};assert.throws(()=>validate(encode(e)));});
test('wrong package cannot satisfy contract',()=>assert.throws(()=>validate(encode(events().map(e=>({...e,Package:'unrelated'}))))));
test('missing and repeated contract refused',()=>{const e=events();e.splice(0,2);assert.throws(()=>validate(encode(e)));const f=events();f.splice(-1,0,...f.slice(0,2));assert.throws(()=>validate(encode(f)));});
test('malformed and truncated stream refused',()=>{assert.throws(()=>validate('{\n'));assert.throws(()=>validate(encode(events()).trimEnd()));});
test('package completion and terminal ordering required',()=>{const e=events();e.pop();assert.throws(()=>validate(encode(e)));assert.throws(()=>validate(encode([...events(),{Action:'output',Package,Output:'late'}])));});

test('Go1.26 build output cannot contribute passes',()=>{
 validate(encode([{Action:'build-output',ImportPath:'p',Output:'compiler note'},...events()]));
 assert.throws(()=>validate(encode([{Action:'build-output',ImportPath:'p',Output:'PASS'}])));
});
