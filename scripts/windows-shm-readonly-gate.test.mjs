import test from 'node:test';
import assert from 'node:assert/strict';
import {validate,required} from './windows-shm-readonly-gate.mjs';
const pkg='agent/internal/nativefs/windowsprobe';
function events(){return [{Action:'start',Package:pkg},...Array.from({length:3},()=>required.flatMap(Test=>[{Action:'run',Package:pkg,Test},{Action:'output',Package:pkg,Test,Output:'SHM_PRIMITIVE_CONFIRMED '+Test+'\n'},{Action:'pass',Package:pkg,Test}])).flat(),{Action:'pass',Package:pkg}];}
const encode=e=>e.map(x=>JSON.stringify(x)).join('\n')+'\n';
test('exact seven primitives three rounds',()=>{assert.equal(required.length,7);validate(encode(events()));});
test('reject fail skip missing unpaired and after finish',()=>{
 for(const Action of ['fail','skip','build-fail']) assert.throws(()=>validate(encode([...events(),{Action,ImportPath:pkg}])));
 let e=events();e.splice(2,1);assert.throws(()=>validate(encode(e)));
 e=events();e[1].Action='pass';assert.throws(()=>validate(encode(e)));
 assert.throws(()=>validate(encode([...events(),{Action:'run',Package:pkg,Test:required[0]}])));
 assert.throws(()=>validate(encode(events()).trim()));
});
test('build output cannot count as test pass',()=>{
 validate(encode([{Action:'build-output',ImportPath:pkg,Output:'PASS\n'},...events()]));
 assert.throws(()=>validate(encode([{Action:'build-output',ImportPath:pkg,Output:'PASS\n'},{Action:'pass',Package:pkg}])));
});
test('Go build lifecycle is not test evidence',()=>{
 validate(encode([{Action:'build-start',ImportPath:pkg},{Action:'build-output',ImportPath:pkg,Output:'compiler output\n'},{Action:'build-done',ImportPath:pkg},...events()]));
 for(const e of [{Action:'build-fail',ImportPath:pkg},{Action:'build-start',ImportPath:pkg,Test:required[0]},{Action:'build-done'}]) assert.throws(()=>validate(encode([e,...events()])));
});
