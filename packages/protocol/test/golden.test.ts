import {test} from 'node:test';import assert from 'node:assert/strict';import {readFile} from 'node:fs/promises';
import {emptyState,reduceSession,parseFrame} from '../src/index.ts';
import type {Snapshot,WireEvent} from '../src/index.ts';
test('Go v1 golden snapshot plus replay yields identical TS presentation',async()=>{const f=JSON.parse(await readFile(new URL('../../../protocol/v1/replay.golden.json',import.meta.url),'utf8')) as {snapshot:Snapshot;replay:WireEvent[];final:Snapshot};let state=reduceSession(emptyState('s'),parseFrame('snapshot',JSON.stringify(f.snapshot)));for(const event of f.replay)state=reduceSession(state,parseFrame('event',JSON.stringify(event)));assert.equal(state.cursor,f.final.cursor);assert.deepEqual(state.runs,f.final.runs);assert.equal(state.needsSnapshot,false)});

test('Go recovered-journal cancellation fixture preserves completed effect in TS replay',async()=>{const f=JSON.parse(await readFile(new URL('../../../protocol/v1/recovery.golden.json',import.meta.url),'utf8')) as {snapshot:Snapshot;replay:WireEvent[];final:Snapshot};let state=reduceSession(emptyState('s'),parseFrame('snapshot',JSON.stringify(f.snapshot)));for(const event of f.replay)state=reduceSession(state,parseFrame('event',JSON.stringify(event)));assert.equal(state.cursor,f.final.cursor);assert.deepEqual(state.runs,f.final.runs);assert.equal(state.runs[0]?.state,'cancelled');assert.equal(state.runs[0]?.tools[1]?.result,'applied exact replacement')});

test('Go exec review and uncertain outcome preserve exact command and never claim stopped',async()=>{
 const {parseApprovalReview}=await import('../src/index.ts');
 const fixture=JSON.parse(await readFile(new URL('../../../protocol/v1/exec.golden.json',import.meta.url),'utf8'));
 const review=parseApprovalReview(fixture.review);assert.equal(review.kind,'exec');if(review.kind!=='exec')throw new Error('wrong kind');assert.deepEqual(review.exec.argv,['%s','hello 世界']);assert.match(review.exec.warning,/NON-SANDBOXED/);
 let state=reduceSession(emptyState('s'),parseFrame('snapshot',JSON.stringify(fixture.snapshot)));
 for(const event of fixture.replay)state=reduceSession(state,parseFrame('event',JSON.stringify(event)));
 assert.deepEqual(state.runs,fixture.final.runs);assert.equal(state.runs[0]?.tools[0]?.state,'unknown');assert.equal(state.runs[0]?.phase,'needs_reconciliation');
});

test('MCP exact tagged review rejects mixed and incomplete arms',async()=>{
 const {parseApprovalReview}=await import('../src/index.ts');
 const review={kind:'mcp',mcp:{server_id:'owned',tool:'write',arguments:'{"value":"hello"}',schema_digest:'schema',config_fingerprint:'config',protocol_version:'2026-07-28',generation:'generation',command:'/owned/helper',environment_policy:'declared only',effect_class:'unknown/writable',warning:'NON-SANDBOXED: current-user code',unavailable_reason:''}};
 assert.deepEqual(parseApprovalReview(review),review);
 for(const bad of [{...review,edit:{}},{...review,exec:{}},{kind:'mcp',mcp:{}},{...review,mcp:{...review.mcp,warning:'safe sandbox'}},{...review,mcp:{...review.mcp,unavailable_reason:undefined}}])assert.throws(()=>parseApprovalReview(bad));
});

test('Go MCP exact review and uncertain effect replay agree with TS',async()=>{
 const {parseApprovalReview}=await import('../src/index.ts');
 const fixture=JSON.parse(await readFile(new URL('../../../protocol/v1/mcp.golden.json',import.meta.url),'utf8'));
 const review=parseApprovalReview(fixture.review);assert.equal(review.kind,'mcp');if(review.kind!=='mcp')throw new Error('wrong kind');assert.equal(review.mcp.arguments,'{"value":"hello 世界"}');assert.equal(review.mcp.effect_class,'unknown/writable');
 let state=reduceSession(emptyState('s'),parseFrame('snapshot',JSON.stringify(fixture.snapshot)));
 for(const event of fixture.replay)state=reduceSession(state,parseFrame('event',JSON.stringify(event)));
 assert.deepEqual(state.runs,fixture.final.runs);assert.equal(state.runs[0]?.phase,'needs_reconciliation');assert.equal(state.runs[0]?.tools[0]?.state,'unknown');
});
