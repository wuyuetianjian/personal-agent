import test from 'node:test';
import assert from 'node:assert/strict';
import {validate} from './windows-stock-alias-gate.mjs';
const Package='agent/internal/nativefs/windowsprobe',Test='TestStockOwnerAliasDiagnostic';
function stream(marker='STOCK_ALIAS_GAP_CONFIRMED'){let e=[{Package,Action:'start'}];for(let i=0;i<3;i++){e.push({Package,Test,Action:'run'});if(marker)e.push({Package,Test,Action:'output',Output:marker+'\n'});e.push({Package,Test,Action:'pass'});}e.push({Package,Action:'pass'});return e.map(JSON.stringify).join('\n')+'\n';}
test('three diagnosed rounds accept gap or precise stock refusal',()=>{assert.doesNotThrow(()=>validate(stream()));assert.doesNotThrow(()=>validate(stream('STOCK_ALIAS_SQLITE_REJECTED')));});
test('PASS without fact result, child, skipped, fail or truncated refuse',()=>{for(const s of [stream(''),stream().replace(Test,'TestStockOwnerAliasChild'),stream().replace('"pass"','"skip"'),stream().replace('"pass"','"fail"'),stream().trimEnd()])assert.throws(()=>validate(s));});
test('contradictory/double branch and unpaired pass refuse',()=>{assert.throws(()=>validate(stream('STOCK_ALIAS_GAP_CONFIRMED STOCK_ALIAS_SQLITE_REJECTED')));assert.throws(()=>validate(stream().replace('"run"','"output"')));});

test('successful alias with no alternate files observed is a separate factual branch',()=>assert.doesNotThrow(()=>validate(stream('STOCK_ALIAS_NO_ALTERNATE_OBSERVED'))));
test('duplicated same marker within one event and falsey Test cannot satisfy',()=>{assert.throws(()=>validate(stream('STOCK_ALIAS_GAP_CONFIRMED STOCK_ALIAS_GAP_CONFIRMED')));const s=stream();assert.throws(()=>validate(s.replace(/\{"Package":"agent\/internal\/nativefs\/windowsprobe","Action":"pass"\}\n$/,'{"Package":"'+Package+'","Action":"pass","Test":""}\n')));});
