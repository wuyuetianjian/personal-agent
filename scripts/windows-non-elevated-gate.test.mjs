import {test} from 'node:test';
import assert from 'node:assert/strict';
import {validateToken,validate} from './windows-non-elevated-gate.mjs';
import {required as lease} from './windows-lease-gate.mjs';
import {required as permit} from './windows-permit-gate.mjs';
const pkg='agent/internal/nativefs/windowsprobe';
const snapshot={Elevation:0,ElevationType:1,Integrity:'S-1-16-8192',AdminAttributes:[16],EnabledPrivileges:['SeChangeNotifyPrivilege']};
const guard=()=>({Action:'output',Package:pkg,Output:'TOKEN_GUARD_OK '+JSON.stringify(snapshot)+'\n'});
const text=events=>events.map(e=>JSON.stringify(e)+'\n').join('');
function events(){return [{Action:'start',Package:pkg},guard(),...Array.from({length:3},()=>[...lease,...permit].flatMap(Test=>[{Action:'run',Package:pkg,Test},{Action:'pass',Package:pkg,Test}])).flat(),{Action:'pass',Package:pkg}];}
test('actual medium non-elevated snapshot only',()=>{
 validateToken(snapshot);validateToken({...snapshot,AdminAttributes:null,EnabledPrivileges:null});validateToken({...snapshot,ElevationType:3,AdminAttributes:[],EnabledPrivileges:[]});
 for(const bad of [{Elevation:1},{ElevationType:2},{Integrity:'S-1-16-12288'},{AdminAttributes:[20]},{AdminAttributes:[0]},{EnabledPrivileges:['SeDebugPrivilege']},{Unknown:true}]) assert.throws(()=>validateToken({...snapshot,...bad}));
});
test('one pre-test token guard plus all 22 native nodes three rounds',()=>{assert.equal(lease.length+permit.length,22);validate(text(events()));});
test('missing, duplicate, post-run or named-test guard refuses',()=>{
 const e=events();assert.throws(()=>validate(text(e.filter(x=>x!==e[1]))));
 assert.throws(()=>validate(text([e[0],guard(),...e.slice(1)])));
 assert.throws(()=>validate(text([e[0],e[2],e[1],...e.slice(3)])));
 assert.throws(()=>validate(text([e[0],{...guard(),Test:lease[0]},...e.slice(2)])));
});
test('failed skipped partial extra and fake text results refuse',()=>{
 for(const Action of ['fail','skip','build-fail']) assert.throws(()=>validate(text([{Action,Package:pkg}])));
 assert.throws(()=>validate(text([guard(),{Action:'output',Package:pkg,Output:'PASS'}])));
 assert.throws(()=>validate(text(events().slice(0,-1))));
 const e=events();e.splice(2,0,{Action:'run',Package:pkg,Test:'TestUnlisted'});assert.throws(()=>validate(text(e)));
});

test('token claim with elevated actual fields cannot qualify events',()=>{const e=events();e[1].Output='TOKEN_GUARD_OK '+JSON.stringify({...snapshot,Elevation:1})+'\n';assert.throws(()=>validate(text(e)));});
