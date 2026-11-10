import {test} from 'node:test';
import assert from 'node:assert/strict';
import {AgentClient,QueueOperations,SessionController,ContextOperations} from '../src/index.ts';
import {protocolFlavor} from '@personal-agent/protocol';
const item={id:'item',session_id:'s',request_id:'q',content:'original  text',state:'pending',run_id:'',position:1,phase:'',blocked_reason:'previous_run:model',policy_digest:'digest'};
const record={schema_version:1,session_id:'s',request_id:'q',kind:'enqueue',item};
function clientWith(fetcher:typeof fetch){return new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 'test',fetch:async(u,i)=>String(u).endsWith('/status')?Response.json({protocol:protocolFlavor}):fetcher(u,i)})}
test('queue response loss retains identity, blocks repeated enqueue/message/context; GET recovery never writes',async()=>{
 let writes=0,ids=0,saved:string|null=null;
 const client=clientWith(async(_u,i)=>{if(i?.method==='POST'){writes++;throw new Error('lost')}return Response.json(record)});
 const ops=new QueueOperations(client,{id:()=>{ids++;return 'q'},save:v=>{saved=v}});const c=new SessionController(client,'s',new ContextOperations(client),ops);
 c.setDraft(item.content);await assert.rejects(c.enqueueDraft(),/lost/);assert.equal(c.draft,item.content);assert.equal(ops.locked,true);assert.ok(saved);assert.ok(!String(saved).includes(item.content),'persist only identity, not draft');
 await c.enqueueDraft();await c.submit();await c.contextCommand('clear',['confirm']);assert.equal(writes,1);assert.equal(ids,1);
 await ops.recover();assert.equal(ops.phase,'confirmed');ops.applied('other');assert.equal(ops.locked,true);ops.applied('s');assert.equal(ops.locked,false);assert.equal(writes,1);
});
test('explicit retry uses same frozen payload and ID; restored identity only never auto retries',async()=>{
 const writes:unknown[]=[];let saved:string|null=null;const client=clientWith(async(_u,i)=>{if(i?.method==='POST'){writes.push(JSON.parse(String(i.body)));if(writes.length===1)throw new Error('lost');return Response.json(record)}return Response.json({error:{code:'queue_operation_not_found'}},{status:404})});
 const ops=new QueueOperations(client,{id:()=> 'q',save:v=>{saved=v}});await assert.rejects(ops.enqueue('s',item.content));await ops.retry();assert.deepEqual(writes[0],writes[1]);assert.equal(ops.result?.item.id,'item');
 const restored=new QueueOperations(client,{restore:saved});await assert.rejects(restored.recover());assert.equal(restored.locked,true);await assert.rejects(restored.retry(),/original payload/);assert.equal(writes.length,2);
});
test('queue acknowledgement rejects wrong owner or mismatched content and stays unknown',async()=>{
 for(const bad of [{...record,session_id:'other'},{...record,item:{...item,content:'changed'}},{...record,item:{...item,state:'admitted',run_id:''}}]){const ops=new QueueOperations(clientWith(async()=>Response.json(bad)),{id:()=> 'q'});await assert.rejects(ops.enqueue('s',item.content));assert.equal(ops.phase,'unknown')}
});
test('queue precommit rejection releases quarantine, generic storage errors do not',async()=>{
 for(const [code,locked] of [['queue_operation_rejected',false],['storage_error',true]] as const){const ops=new QueueOperations(clientWith(async()=>Response.json({error:{code}},{status:409})));await assert.rejects(ops.enqueue('s','text'));assert.equal(ops.locked,locked)}
});
test('busy active run allows only explicit enqueue and keeps newer draft while pending',async()=>{
 let release!:(r:Response)=>void,writes=0;const client=clientWith(async(u,i)=>{if(i?.method==='POST'){writes++;return new Promise(r=>{release=r})}return Response.json({schema_version:1,session_id:'s',limit:16,items:[item]})});
 const ops=new QueueOperations(client,{id:()=> 'q'});const c=new SessionController(client,'s',undefined,ops);c.state.runs=[{id:'active',request_id:'a',input:'active',state:'running',phase:'model',text:'',draft:'',tools:[]}];c.setDraft(item.content);await c.submit();assert.equal(writes,0);const pending=c.enqueueDraft();await new Promise(r=>setTimeout(r,0));c.setDraft('next draft');await c.enqueueDraft();assert.equal(writes,1);release(Response.json(record));await pending;assert.equal(c.draft,'next draft');assert.equal(c.queue?.items.length,1);assert.equal(ops.locked,false);
});
test('withdraw response loss keeps the target and GET recovery confirms only that operation',async()=>{
 let writes=0,saved:string|null=null;const result={...record,request_id:'withdraw',kind:'withdraw',item:{...item,state:'withdrawn',position:0}};
 const client=clientWith(async(_u,i)=>{if(i?.method==='POST'){writes++;throw new Error('lost')}return Response.json(result)});const ops=new QueueOperations(client,{id:()=> 'withdraw',save:v=>{saved=v}});await assert.rejects(ops.withdraw('s','item'));await ops.withdraw('s','other');assert.equal(writes,1);const restored=new QueueOperations(client,{restore:saved});assert.equal((await restored.recover())?.item.id,'item');restored.applied('s');assert.equal(restored.locked,false);
});
test('restored withdrawal refuses an unrelated item even if server operation ID matches',async()=>{
 const result={...record,request_id:'withdraw',kind:'withdraw',item:{...item,id:'other',state:'withdrawn',position:0}};
 const ops=new QueueOperations(clientWith(async()=>Response.json(result)),{restore:JSON.stringify({session_id:'s',request_id:'withdraw',kind:'withdraw',item_id:'item'})});await assert.rejects(ops.recover(),/mismatch/);assert.equal(ops.phase,'unknown');
});
