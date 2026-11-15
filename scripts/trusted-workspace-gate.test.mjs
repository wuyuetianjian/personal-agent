import test from 'node:test';
import assert from 'node:assert/strict';
import {manifest, validate} from './trusted-workspace-gate.mjs';
const Package='agent/internal/interaction';
const encode=es=>es.map(e=>JSON.stringify(e)).join('\n')+'\n';
for(const target of ['darwin','windows']) {
 const events=()=>[{Action:'start',Package},...Array.from({length:3},()=>manifest(target).flatMap(Test=>[{Action:'run',Package,Test},{Action:'pass',Package,Test}])).flat(),{Action:'pass',Package}];
 test(target+' exact native 6x3 passes',()=>{assert.equal(manifest(target).length,6);validate(encode(events()),target)});
 for(const Action of ['fail','skip','build-fail'])test(target+' rejects '+Action,()=>assert.throws(()=>validate(encode([{Action,Package:'other'},...events()]),target)));
 test(target+' rejects missing or fake PASS',()=>assert.throws(()=>validate(encode(events().filter(e=>e.Test!==manifest(target)[0])),target)));
 test(target+' rejects unknown test',()=>assert.throws(()=>validate(encode(events().map(e=>({...e,...(e.Test?{Test:'unknown'}:{})}))),target)));
 test(target+' rejects truncated JSON',()=>assert.throws(()=>validate(encode(events()).trimEnd(),target)));
 test(target+' accepts build output and CRLF',()=>validate(encode([{Action:'build-output',ImportPath:'p',Output:'compile'},...events()]).replaceAll('\n','\r\n'),target));
}
test('unknown target rejected',()=>assert.throws(()=>manifest('linux')));
