import {test} from 'node:test';
import assert from 'node:assert/strict';
import {SessionController} from '../src/presentation.ts';

test('busy submission retains the draft and never posts',async()=>{
 let posts=0;
 const c=new SessionController({submitMessage:async()=>{posts++;return {run_id:'r',state:'running'}}} as never,'s');
 c.state={...c.state,runs:[{id:'r',request_id:'q',input:'x',state:'running',phase:'model',text:'',draft:'',tools:[]}]};c.draft='下一条';
 await c.submit();assert.equal(posts,0);assert.equal(c.draft,'下一条');
});
test('failed submission preserves draft; successful submission clears only the submitted value',async()=>{
 let finish!:(x:{run_id:string;state:string})=>void;let calls=0;
 const c=new SessionController({submitMessage:()=>{calls++;return new Promise(r=>finish=r)}} as never,'s');c.draft='first';const p=c.submit();c.draft='next';finish({run_id:'r',state:'running'});await p;assert.equal(c.draft,'next');assert.equal(calls,1);
 const failing=new SessionController({submitMessage:async()=>{throw new Error('outcome_unknown_check_snapshot')}} as never,'s');failing.draft='keep';await assert.rejects(failing.submit());assert.equal(failing.draft,'keep');
});
test('approval double click submits only one request',async()=>{
 let finish!:(x:{run_id:string;decision:string})=>void;let calls=0;
 const c=new SessionController({decideApproval:()=>{calls++;return new Promise(r=>finish=r)}} as never,'s');
 const p=c.decide('a','digest','approved');await c.decide('a','digest','approved');finish({run_id:'r',decision:'approved'});await p;assert.equal(calls,1);
});
test('accepted approval stays disabled until the authoritative stream changes',async()=>{
 let calls=0;const c=new SessionController({decideApproval:async()=>{calls++;return {run_id:'r',decision:'approved'}}} as never,'s');
 await c.decide('a','digest','approved');await c.decide('a','digest','approved');assert.equal(calls,1);
});
test('accepted submission remains busy until its run appears in authoritative stream',async()=>{
 const c=new SessionController({submitMessage:async()=>({run_id:'r',state:'running'})} as never,'s');c.draft='first';await c.submit();assert.equal(c.busy,true);c.draft='second';await c.submit();assert.equal(c.draft,'second');
});
test('older pages prepend chronologically without duplicates and do not replace live state on replay',async()=>{
 const run=(id:string)=>({id,request_id:id,input:id,state:'completed',phase:'completed',text:id,draft:'',tools:[]});
 const session={id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''};
 const snapshot=(ids:string[],cursor:number,has_older:boolean,next_before?:number)=>({schema_version:1,session,cursor,runs:ids.map(run),has_older,...(next_before===undefined?{}:{next_before})});
 let unblock!:()=>void;const gate=new Promise<void>(r=>unblock=r);const client={history:async(_id:string,before:number)=>{assert.equal(before,30);return snapshot(['old','overlap'],99,false)},watchSession:async function*(){yield {kind:'snapshot' as const,snapshot:snapshot(['overlap','live'],40,true,30)};await gate;yield {kind:'snapshot' as const,snapshot:snapshot(['live','new'],45,true,40)}}};
 const c=new SessionController(client as never,'s');const watch=c.watch(new AbortController().signal);await new Promise(r=>setTimeout(r,0));await c.loadOlder();assert.deepEqual(c.runs.map(r=>r.id),['old','overlap','live']);assert.equal(c.hasOlder,false);unblock();await watch;assert.deepEqual(c.runs.map(r=>r.id),['old','overlap','live','new']);assert.equal(c.state.cursor,45);
});
test('concurrent older-history requests are coalesced',async()=>{let calls=0,finish!:(x:unknown)=>void;const c=new SessionController({history:()=>{calls++;return new Promise(r=>finish=r)},watchSession:async function*(){yield {kind:'snapshot',snapshot:{schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''},cursor:1,runs:[],has_older:true,next_before:1}}}} as never,'s');await c.watch(new AbortController().signal);const a=c.loadOlder(),b=c.loadOlder();assert.equal(calls,1);finish({schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''},cursor:1,runs:[],has_older:false});await Promise.all([a,b]);});
test('session pagination keeps first-seen order and deduplicates overlapping pages',async()=>{const {mergeSessionPage}=await import('../src/presentation.ts');const s=(id:string)=>({id,model_id:'m',mode:'chat_only',files:false,tools:false,reason:''});assert.deepEqual(mergeSessionPage([s('new'),s('shared')],[s('shared'),s('old')]).map(x=>x.id),['new','shared','old'])});
test('unknown message outcome stays locked until explicit snapshot proves the original request',async()=>{
 let requestID='',posts=0;const run={id:'r',request_id:'',input:'keep',state:'completed',phase:'completed',text:'done',draft:'',tools:[]};
 const c=new SessionController({submitMessage:async(_s:string,_v:string,q:string)=>{requestID=q;posts++;throw new Error('lost response')},snapshot:async()=>({schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''},cursor:1,runs:run.request_id?[run]:[],has_older:false})} as never,'s');c.draft='keep';await assert.rejects(c.submit());await c.submit();assert.equal(posts,1);await c.reconcile();assert.equal(c.busy,true);run.request_id=requestID;await c.reconcile();assert.equal(c.busy,false);assert.equal(c.draft,'');assert.equal(c.runs.length,1);assert.equal(posts,1);
});
for(const label of ['macOS','Windows','unsupported target "freebsd"']) {
 test(`capability summary reports exact disabled native reasons for ${label}`,async()=>{
  const {capabilitySummary}=await import('../src/presentation.ts');
  const files='Native file bundle unavailable (Read, Search, Replace, Undo required): Native file adapter unavailable on '+label+': file identity, containment, read, replacement and undo are not natively qualified';
  const exec='NON-SANDBOXED exec unavailable on '+label+': executable identity and direct-child cleanup are not natively qualified';
  const status={workspace:{Files:{Enabled:false,Reason:files},Exec:{Enabled:false,Reason:exec}}};
  const before=structuredClone(status);
  assert.equal(capabilitySummary(status),`文件工具不可用：${files} · 命令执行不可用：${exec}`);
  assert.deepEqual(status,before);assert.equal(status.workspace.Files.Enabled,false);assert.equal(status.workspace.Exec.Enabled,false);
 });
}
test('explicit snapshot clears an acknowledged run wait without needing a live stream',async()=>{
 const c=new SessionController({submitMessage:async()=>({run_id:'r',state:'running'}),snapshot:async()=>({schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''},cursor:4,runs:[{id:'r',request_id:'q',input:'one',state:'completed',phase:'completed',text:'done',draft:'',tools:[]}],has_older:false})} as never,'s');c.draft='one';await c.submit();assert.equal(c.busy,true);await c.reconcile();assert.equal(c.busy,false);
});
test('definitive pre-admission message rejection preserves draft and unlocks composer',async()=>{const {APIError}=await import('../src/index.ts');for(const [status,code] of [[400,'invalid_content'],[503,'service_capacity']] as const){let calls=0;const c=new SessionController({submitMessage:async()=>{calls++;throw new APIError(status,code)}} as never,'s');c.draft='fixable';await assert.rejects(c.submit());assert.equal(c.draft,'fixable');assert.equal(c.busy,false);assert.equal(c.needsReconciliation,false);await assert.rejects(c.submit());assert.equal(calls,2)}});
test('HTTP status alone cannot classify a write as definitely rejected',async()=>{const {APIError}=await import('../src/index.ts');for(const error of [new APIError(400,'outcome_unknown_check_snapshot'),new APIError(503,'other'),new Error('transport lost')]){const c=new SessionController({submitMessage:async()=>{throw error}} as never,'s');c.draft='x';await assert.rejects(c.submit());assert.equal(c.needsReconciliation,true);assert.equal(c.busy,true)}});
