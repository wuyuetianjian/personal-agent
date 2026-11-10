import { test } from 'node:test';
import assert from 'node:assert/strict';
import { emptyState, reduceSession, parseFrame } from '../src/index.ts';
const event = (sequence: number, type: string, data: unknown = {}) => ({ schema_version: 1, session_id: 's', run_id: 'r', sequence, type, data });
test('ordered events, duplicate ignored, gap requests snapshot without applying', () => {
 let s = reduceSession(emptyState('s'), { kind: 'event', event: event(1, 'run.created', {request_id:'q',input:'你好',state:'running'}) });
 s = reduceSession(s, {kind:'event',event:event(2,'model.text_delta',{text:'真'})});
 assert.equal(s.runs[0]?.draft,'真');
 assert.deepEqual(reduceSession(s,{kind:'event',event:event(2,'model.text_delta',{text:'真'})}),s);
 const gap=reduceSession(s,{kind:'event',event:event(4,'model.text_delta',{text:'丢失'})});
 assert.equal(gap.needsSnapshot,true); assert.equal(gap.cursor,2);assert.equal(gap.runs[0]?.draft,'真');
});
test('unknown events advance cursor and snapshot replay never resends actions',()=>{
 let s=reduceSession(emptyState('s'),{kind:'event',event:event(1,'future.private',{secret:'ignored'})});assert.equal(s.cursor,1);assert.equal(s.runs.length,0);
 s=reduceSession(s,{kind:'snapshot',snapshot:{schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:'none'},cursor:10,runs:[{id:'r',request_id:'q',input:'hello',state:'running',phase:'model',text:'',draft:'old',tools:[]}],has_older:false}});
 s=reduceSession(s,{kind:'event',event:event(11,'model.completed',{text:'actual'})});assert.equal(s.runs[0]?.text,'actual');assert.equal(s.runs[0]?.draft,'');
 s=reduceSession(s,{kind:'event',event:event(12,'run.cancelling')});assert.equal(s.runs[0]?.state,'running');assert.equal(s.runs[0]?.phase,'cancelling');
 s=reduceSession(s,{kind:'event',event:event(13,'run.cancelled',{state:'cancelled'})});assert.equal(s.runs[0]?.state,'cancelled');
 assert.throws(()=>parseFrame('event',JSON.stringify({...event(14,'x'),schema_version:2})),/schema/);
});
test('rejects wrong session, malformed cursor and prototype pollution input',()=>{
 const s=emptyState('s');assert.throws(()=>reduceSession(s,{kind:'event',event:{...event(1,'x'),session_id:'other'}}),/session/);
 assert.throws(()=>parseFrame('event',JSON.stringify(event(-1,'x'))),/sequence/);
});
test('malformed known events are rejected even for an unknown run',()=>{
 for(const [type,data] of [['model.text_delta',{text:7}],['approval.waiting',{id:'p'}],['run.completed',{state:'running'}],['tool.completed',{content:'x',is_error:'no'}]] as const){assert.throws(()=>parseFrame('event',JSON.stringify(event(1,type,data))),/invalid/)}
});
test('cancelling cannot regress to running when a late tool result arrives',()=>{let s=reduceSession(emptyState('s'),{kind:'event',event:event(1,'run.created',{request_id:'q',input:'x',state:'running'})});s=reduceSession(s,{kind:'event',event:event(2,'run.cancelling')});s=reduceSession(s,{kind:'event',event:event(3,'tool.completed',{content:'observed',is_error:false})});assert.equal(s.runs[0]?.phase,'cancelling')});
test('identity-bound recovered observation updates correct card and preserves cancellation',()=>{
 let s=reduceSession(emptyState('s'),{kind:'event',event:event(1,'run.created',{request_id:'q',input:'edit',state:'running'})});
 for(const [sequence,id] of [[2,'original'],[3,'other']] as const)s=reduceSession(s,{kind:'event',event:event(sequence,'tool.proposed',{id,name:'apply_patch',arguments:'{}',digest:'d',expires_at:'2030-01-01T00:00:00Z',requires_approval:true})});
 s=reduceSession(s,{kind:'event',event:event(4,'run.cancelling')});
 const recovered=event(5,'tool.recovered',{id:'original',call_id:'call',content:'observed result',is_error:false});s=reduceSession(s,{kind:'event',event:recovered});
 assert.equal(s.runs[0]?.tools[0]?.state,'completed');assert.equal(s.runs[0]?.tools[0]?.result,'observed result');assert.equal(s.runs[0]?.tools[1]?.state,'proposed');assert.equal(s.runs[0]?.phase,'cancelling');assert.deepEqual(reduceSession(s,{kind:'event',event:recovered}),s);
});
test('safe tool capability state survives snapshots without granting tools or exec',()=>{
 const snapshot={schema_version:1,session:{id:'s',model_id:'m',mode:'default',files:true,tools:false,reason:'Create a new session',tool_capability:'provider_changed'},cursor:0,runs:[],has_older:false};
 const state=reduceSession(emptyState('s'),parseFrame('snapshot',JSON.stringify(snapshot)));
 assert.equal(state.session?.tool_capability,'provider_changed');assert.equal(state.session?.tools,false);assert.equal(state.session?.non_sandbox_exec,undefined);
 assert.throws(()=>parseFrame('snapshot',JSON.stringify({...snapshot,session:{...snapshot.session,tool_capability:true}})),/invalid/);
});
test('advanced command descriptors explicitly mark unavailable features',async()=>{const {commands}=await import('../src/index.ts');for(const name of ['hooks'])assert.equal(commands.find(c=>c.name===name)?.available,false);for(const name of ['branch','compact','rewind','clear','queue'])assert.equal(commands.find(c=>c.name===name)?.available,true)});
test('terminal proposed tool does not appear pending after its run ends',async()=>{const {toolDisplayState}=await import('../src/index.ts');assert.equal(toolDisplayState('failed','proposed'),'未完成（回合已结束）');assert.equal(toolDisplayState('running','proposed'),'proposed');assert.equal(toolDisplayState('completed','completed'),'completed')});

test('completed tool failure renders as error, while uncertain stays unknown',async()=>{
 const {toolDisplayState}=await import('../src/index.ts');
 assert.equal(toolDisplayState('completed','completed',true),'error');
 assert.equal(toolDisplayState('running','unknown',true),'unknown');
 assert.equal(toolDisplayState('completed','completed',false),'completed');
});

test('session snapshot rejects malformed or authority-bearing project labels',()=>{
 const label={project_id:'metadata-名称',generation:'a'.repeat(32),revision:1,name:'Exact\n名称'};
 const snapshot={schema_version:1,session:{id:'s',model_id:'m',mode:'default',files:true,tools:false,reason:'',project_label:label},cursor:0,runs:[],has_older:false};
 const frame=parseFrame('snapshot',JSON.stringify(snapshot)); assert.equal(frame.kind,'snapshot'); if(frame.kind==='snapshot')assert.deepEqual(frame.snapshot.session.project_label,label);
 for(const bad of [null,{...label,revision:0},{...label,revision:1.5},{...label,revision:Number.MAX_SAFE_INTEGER+1},{...label,generation:'A'.repeat(32)},{...label,generation:''},{...label,project_id:''},{...label,project_id:'a'.repeat(513)},{...label,name:'界'.repeat(683)},{...label,name:'\ud800'},{...label,scope_id:'stolen'},{...label,execution_class:'legacy_policy'}]){
  assert.throws(()=>parseFrame('snapshot',JSON.stringify({...snapshot,session:{...snapshot.session,project_label:bad}})));
 }
});
