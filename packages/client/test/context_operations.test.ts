import {test} from 'node:test';import assert from 'node:assert/strict';
import {AgentClient,ContextOperations,SessionController} from '../src/index.ts';
import {emptyState,protocolFlavor} from '@personal-agent/protocol';
const child={id:'child',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''};
const record={schema_version:1,session_id:'parent',operation:{request_id:'same',kind:'clear',boundary_run_id:'',expected_cursor:0},provenance:{session_id:'child',parent_session_id:'parent',boundary_run_id:'',source_cursor:0,kind:'clear'}};
function clientWith(fetcher:typeof fetch){return new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 'test',fetch:async(u,i)=>String(u).endsWith('/status')?Response.json({protocol:protocolFlavor}):fetcher(u,i)})}
test('lost context response retains exact intent and shared submission lock; recovery is GET only',async()=>{
 const writes:unknown[]=[];let saved:string|null=null;let uuidCalls=0;
 const client=clientWith(async(u,i)=>{if(i?.method==='POST'){writes.push(JSON.parse(String(i.body)));throw new Error('response lost')}if(String(u).endsWith('/operations/same'))return Response.json(record);if(String(u).endsWith('/snapshot'))return Response.json({schema_version:1,session:child,cursor:0,runs:[],has_older:false});throw new Error('unexpected GET')});
 const ops=new ContextOperations(client,{id:()=>{uuidCalls++;return 'same'},save:v=>{saved=v}});const c=new SessionController(client,'parent',ops);
 await assert.rejects(ops.command(c.state,'clear',['confirm']),/lost/);assert.equal(ops.locked,true);assert.equal(ops.pending?.request_id,'same');assert.equal(ops.pending?.session_id,'parent');assert.equal(ops.pending?.expected_cursor,0);assert.ok(String(saved).includes('same'));
 c.setDraft('new-context-only');await c.submit();assert.equal(c.draft,'new-context-only');assert.equal(writes.length,1);
 await ops.command(c.state,'clear',['confirm']);assert.equal(uuidCalls,1);assert.equal(writes.length,1);
 const result=await ops.recover();assert.equal(result?.session?.id,'child');assert.equal(ops.locked,true);ops.applied('parent');assert.equal(ops.locked,true);ops.applied('child');assert.equal(ops.locked,false);assert.equal(saved,null);assert.equal(writes.length,1);
});
test('delayed clear gates ordinary input and navigation until the confirmed child is applied',async()=>{
 let release!:(r:Response)=>void;let writes=0;const client=clientWith(async(_u,i)=>{if(i?.method==='POST'){writes++;return new Promise(r=>{release=r})}throw new Error('unexpected')});
 const ops=new ContextOperations(client,{id:()=> 'same'});const c=new SessionController(client,'parent',ops);let changes=0;ops.subscribe(()=>changes++);
 const pending=ops.command(c.state,'clear',['confirm']);await new Promise(r=>setTimeout(r,0));assert.equal(ops.locked,true);assert.equal(ops.beginNavigation(),null);c.setDraft('keep me');await c.submit();assert.equal(writes,1);assert.equal(c.draft,'keep me');
 release(Response.json(child));const result=await pending;assert.equal(result?.session?.id,'child');assert.equal(ops.locked,true);assert.ok(changes>=2);ops.applied('child');assert.equal(ops.locked,false);
});
test('restart restores unknown identity and read-only missing lookup never unlocks or retries',async()=>{
 let posts=0;const client=clientWith(async(_u,i)=>{if(i?.method==='POST')posts++;return Response.json({error:{code:'operation_not_recorded'}},{status:404})});
 const ops=new ContextOperations(client,{restore:JSON.stringify({...record.operation,session_id:'parent'})});assert.equal(ops.phase,'unknown');await assert.rejects(ops.recover());assert.equal(ops.locked,true);assert.equal(ops.pending?.request_id,'same');assert.equal(posts,0);
});
test('earlier navigation cannot apply after a context operation begins',async()=>{
 const ops=new ContextOperations(clientWith(async()=>Response.json(child)),{id:()=> 'same'});const generation=ops.beginNavigation();assert.notEqual(generation,null);await ops.command(emptyState('parent'),'clear',['confirm']);ops.applied('child');assert.equal(ops.isNavigationCurrent(generation!),false);
});
test('explicit recovery retry reuses the frozen intent and never allocates another UUID',async()=>{
 const bodies:unknown[]=[];let attempts=0;let ids=0;const client=clientWith(async(_u,i)=>{bodies.push(JSON.parse(String(i?.body)));if(attempts++===0)throw new Error('lost before recording');return Response.json(child)});
 const ops=new ContextOperations(client,{id:()=>{ids++;return 'same'}});await assert.rejects(ops.command(emptyState('parent'),'clear',['confirm']));await ops.retry();assert.equal(ids,1);assert.deepEqual(bodies[0],bodies[1]);assert.equal(ops.result?.session?.id,'child');assert.equal(ops.phase,'confirmed');
});
test('only explicit pre-commit rejection clears quarantine, not generic HTTP errors',async()=>{
 for(const [code,locked] of [['context_operation_rejected',false],['storage_error',true]] as const){const ops=new ContextOperations(clientWith(async()=>Response.json({error:{code}},{status:409})),{id:()=> 'same'});await assert.rejects(ops.command(emptyState('parent'),'clear',['confirm']));assert.equal(ops.locked,locked)}
});
test('late read-only context view cannot confirm or release a different in-flight mutation',async()=>{
 let viewDone!:(r:Response)=>void,writeDone!:(r:Response)=>void;const client=clientWith(async(u,i)=>{if(i?.method==='POST')return new Promise(r=>{writeDone=r});if(String(u).endsWith('/context-view'))return new Promise(r=>{viewDone=r});throw new Error('unexpected')});
 const ops=new ContextOperations(client,{id:()=> 'same'});const state=emptyState('parent');const view=ops.command(state,'compact',['view']);await new Promise(r=>setTimeout(r,0));const change=ops.command(state,'clear',['confirm']);await new Promise(r=>setTimeout(r,0));viewDone(Response.json({schema_version:1,session_id:'parent',selection:'original',warning:'',original:[],selected:[]}));await view;assert.equal(ops.phase,'sending');ops.applied('parent');assert.equal(ops.locked,true);writeDone(Response.json(child));await change;assert.equal(ops.result?.session?.id,'child');
});
test('a pending ordinary submission also blocks a context mutation before its run reaches the stream',async()=>{
 let complete!:(r:Response)=>void;let writes=0;let ids=0;const client=clientWith(async()=>{writes++;return new Promise(r=>{complete=r})});const ops=new ContextOperations(client,{id:()=>{ids++;return 'same'}});const c=new SessionController(client,'parent',ops);c.setDraft('first input');const sending=c.submit();await new Promise(r=>setTimeout(r,0));const result=await c.contextCommand('clear',['confirm']);assert.match(result?.notice??'',/busy/);assert.equal(writes,1);assert.equal(ids,0);complete(Response.json({run_id:'run',state:'running'}));await sending;
});
for(const kind of ['compact','original'] as const)test(`recover ${kind} from another open session targets its source, GET only`,async()=>{
 const methods:string[]=[];const source={...child,id:'source'};
 const client=clientWith(async(u,i)=>{methods.push(i?.method??'GET');if(String(u).endsWith('/operations/q'))return Response.json({schema_version:1,session_id:'source',operation:{request_id:'q',kind,boundary_run_id:'',expected_cursor:0},version:{id:kind==='compact'?'version':'',session_id:kind==='compact'?'source':'',source_cursor:0,method:kind==='compact'?'local-omit-resolved-v1':'',omitted_messages:0}});if(String(u).endsWith('/sessions/source/snapshot'))return Response.json({schema_version:1,session:source,cursor:0,runs:[],has_older:false});throw new Error('unexpected request')});
 const ops=new ContextOperations(client);const result=await ops.recover('source','q');assert.equal(result?.session?.id,'source');assert.equal(ops.locked,true);ops.applied('different-open-session');assert.equal(ops.locked,true,'wrong mounted session released target guard');ops.applied('source');assert.equal(ops.locked,false);assert.ok(methods.every(m=>m==='GET'));assert.equal(methods.length,2);
});
for(const kind of ['compact','original'] as const)test(`explicit retry of recovered ${kind} intent also carries the source target`,async()=>{
 const client=clientWith(async(u,i)=>{if(i?.method==='POST'){const body=JSON.parse(String(i.body));assert.equal(body.request_id,'q');assert.equal(body.selection,kind==='compact'?'compacted':'original');return Response.json({id:'',session_id:'source',source_cursor:0,method:'',omitted_messages:0})}if(String(u).endsWith('/sessions/source/snapshot'))return Response.json({schema_version:1,session:{...child,id:'source'},cursor:0,runs:[],has_older:false});throw new Error('unexpected')});
 const ops=new ContextOperations(client,{restore:JSON.stringify({session_id:'source',request_id:'q',kind,boundary_run_id:'',expected_cursor:0})});const result=await ops.retry();assert.equal(result?.session?.id,'source');ops.applied('other');assert.equal(ops.locked,true);ops.applied('source');assert.equal(ops.locked,false);
});
test('file undo retains exact checkpoint through unknown response and GET-only recovery',async()=>{
 let writes=0;const source={...child,id:'parent',mode:'acceptEdits',files:true,tools:true};const op={request_id:'same',kind:'file_undo',boundary_run_id:'checkpoint',expected_cursor:0};
 const client=clientWith(async(u,i)=>{if(i?.method==='POST'){writes++;assert.ok(String(u).endsWith('/file-undo'));assert.equal(JSON.parse(String(i.body)).checkpoint_id,'checkpoint');throw new Error('lost undo acknowledgement')};if(String(u).endsWith('/operations/same'))return Response.json({schema_version:1,session_id:'parent',operation:op,action_run_id:'undo-run'});if(String(u).endsWith('/snapshot'))return Response.json({schema_version:1,session:source,cursor:1,runs:[],has_older:false});throw new Error('unexpected')});
 const ops=new ContextOperations(client,{id:()=> 'same'});await assert.rejects(ops.command(emptyState('parent'),'rewind',['files','checkpoint']),/lost undo/);assert.equal(ops.pending?.kind,'file_undo');assert.equal(ops.locked,true);await ops.command(emptyState('parent'),'rewind',['files','checkpoint']);assert.equal(writes,1);const result=await ops.recover();assert.equal(result?.session?.id,'parent');ops.applied('parent');assert.equal(ops.locked,false);assert.equal(writes,1);
});
test('checkpoint discovery is GET only and validates owning session and byte review',async()=>{
 let writes=0;const client=clientWith(async(_u,i)=>{if(i?.method==='POST')writes++;return Response.json({schema_version:1,session_id:'wrong',supported:true,reason:'limits',checkpoints:[]})});await assert.rejects(client.editCheckpoints('parent'),/session/);assert.equal(writes,0);
});
