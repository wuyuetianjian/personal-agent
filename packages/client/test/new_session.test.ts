import {protocolFlavor} from '@personal-agent/protocol';
import {test} from 'node:test';import assert from 'node:assert/strict';
import {AgentClient,NewSessionOperations,SessionController,parseActiveProjects,projectLabelText,displayJSON} from '../src/index.ts';
const label={project_id:'label',name:'exact\n\u001b[2J\u202ename',generation:'a'.repeat(32),revision:1};
const session={id:'child',model_id:'m',mode:'default',files:true,tools:true,reason:'',project_label:label};
function client(fetcher:typeof fetch){return new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 'secret',fetch:(u,i)=>String(u).endsWith('/protocol')?Promise.resolve(Response.json({protocol:protocolFlavor})):fetcher(u,i)})}
test('one frozen admission, no status resolution or POST replay; GET snapshot reconciles exact label',async()=>{
 const calls:string[]=[];let body:any,saved:string|null=null;
 const c=client(async(u,i)=>{calls.push((i?.method??'GET')+' '+u);if(i?.method==='POST'){body=JSON.parse(String(i.body));throw Error('lost')};if(String(u).includes('/admissions/'))return Response.json({request_id:'original',session_id:'child',state:'complete',mcp_opt_in:true,reason:'',project_label:label});return Response.json({schema_version:1,session,cursor:0,runs:[],has_older:false})});
 const ops=new NewSessionOperations(c,{id:()=> 'original',save:v=>saved=v});const nav=ops.beginNavigation()!;
 const intent={mode:'default' as const,model_id:'m',mcp:true,catalog_project_id:'label',content:'private initial'};
 await assert.rejects(ops.create(intent),/lost/);intent.catalog_project_id='different';assert.equal(ops.pending?.catalog_project_id,'label');assert.equal(ops.pending?.content,'private initial');assert.equal(ops.locked,true);assert.equal(ops.beginNavigation(),null);
 assert.deepEqual(body,{mode:'default',model_id:'m',mcp:true,catalog_project_id:'label',request_id:'original'});
 assert.ok(saved&&!String(saved).includes('private')&&!String(saved).includes('label')&&!String(saved).includes('secret'));
 await assert.rejects(ops.create(intent),/intent unresolved/);assert.equal(calls.length,1);assert.equal((await ops.recover())?.id,'child');await assert.rejects(ops.recover('different'),/different admission/);assert.equal(ops.locked,true);ops.applied('wrong');assert.equal(ops.locked,true);ops.applied('child');assert.equal(ops.locked,false);assert.equal(ops.isNavigationCurrent(nav),false);assert.equal(calls.filter(x=>x.startsWith('POST')).length,1);assert.ok(!calls.some(x=>x.endsWith('/status')));
});
test('admission quarantines every controller mutation and preserves draft/live approval',async()=>{
 let writes=0;const c=client(async()=>{writes++;throw Error('lost')});const ops=new NewSessionOperations(c,{id:()=> 'original'});const view=new SessionController(c,'parent',undefined,undefined,undefined,undefined,undefined,ops);await assert.rejects(ops.create({mode:'chat_only',mcp:false,catalog_project_id:'',content:''}));view.setDraft('keep');await view.submit();await view.enqueueDraft();await view.decide('approval','digest','approved');await view.jobsCommand('/jobs add no');assert.equal(writes,1);assert.equal(view.draft,'keep');assert.equal(view.knowledgeBlocked,true);assert.equal(view.operatorBlocked,true);
});
test('restored IDs only, unresolved/notfound/cancel stay quarantined; explicit acknowledgement only after settled claim',async()=>{
 let state='starting';const c=client(async()=>state==='notfound'?Response.json({error:{code:'admission_not_found'}},{status:404}):Response.json({request_id:'original',session_id:'child',state,reason:''}));
 const ops=new NewSessionOperations(c,{restore:'{"request_id":"original"}'});assert.equal(await ops.recover(),null);ops.cancel();assert.equal(ops.locked,true);assert.throws(()=>ops.abandon(),/unresolved/);state='notfound';await assert.rejects(ops.recover());assert.equal(ops.phase,'starting');const missing=new NewSessionOperations(c,{restore:'{"request_id":"original"}'});await assert.rejects(missing.recover());assert.equal(missing.phase,'not_found');assert.throws(()=>ops.abandon(),/unresolved/);state='revoked';await ops.recover();ops.abandon();assert.equal(ops.locked,false);
});
test('known complete survives storage failure and failed snapshot without losing identity',async()=>{
 let snapshot=false;const c=client(async u=>String(u).includes('/admissions/')?Response.json({request_id:'original',session_id:'child',state:'complete',mcp_opt_in:false,reason:'',project_label:label}):snapshot?Response.json({schema_version:1,session,cursor:0,runs:[],has_older:false}):Promise.reject(Error('offline')));
 const ops=new NewSessionOperations(c,{restore:'{"request_id":"original"}',save:()=>{throw Error('storage unavailable')}});await assert.rejects(ops.recover(),/offline/);assert.equal(ops.sessionID,'child');assert.deepEqual(ops.admission?.project_label,label);snapshot=true;await ops.recover();ops.applied('child');assert.equal(ops.result?.id,'child');assert.equal(ops.locked,false);assert.match(ops.storageWarning,/storage/i);
});
test('mismatch does not adopt another snapshot; only definite preclaim refusal unlocks',async()=>{
 const c=client(async(u,i)=>i?.method==='POST'?Promise.reject(Error('lost')):String(u).includes('/admissions/')?Response.json({request_id:'original',session_id:'child',state:'complete',mcp_opt_in:false,reason:'',project_label:label}):Response.json({schema_version:1,session:{...session,project_label:{...label,name:'wrong'}},cursor:0,runs:[],has_older:false}));const ops=new NewSessionOperations(c,{id:()=> 'original'});await assert.rejects(ops.create({mode:'default',mcp:false,catalog_project_id:'label',content:''}));await assert.rejects(ops.recover(),/mismatch/);assert.equal(ops.locked,true);
 for(const [code,locked] of [['project_label_unavailable',false],['provider_unavailable',true],['request_id_conflict',true]] as const){const x=new NewSessionOperations(client(async()=>Response.json({error:{code}},{status:409})));await assert.rejects(x.create({mode:'chat_only',mcp:false,catalog_project_id:'',content:''}));assert.equal(x.locked,locked,code)}
});
test('bounded active catalog rejects authority, duplicate identity, archived and unsafe Unicode; display escapes not removes',async()=>{
 const item={...label,execution_class:'metadata_only'};const page={schema_version:1,items:[item],next_cursor:''};assert.deepEqual(parseActiveProjects(page),page);for(const bad of [{...page,root:'/tmp'},{...page,items:[item,item]},{...page,items:[{...item,state:'archived'}]},{...page,items:[{...item,execution_class:'unknown'}]},{...page,items:[{...item,name:'\ud800'}]}])assert.throws(()=>parseActiveProjects(bad));assert.match(projectLabelText(label),/\\u001b/);assert.match(projectLabelText(label),/\\u202e/);
 let calls=0;const c=client(async(u,i)=>{calls++;assert.match(String(u),/\/catalog\/projects\?limit=25&cursor=abc/);assert.equal(i?.method,undefined);return Response.json(page)});await c.activeProjects('abc');assert.equal(calls,1);
});
test('recovery must prove original MCP opt-in, not infer it from live process health',async()=>{
 const c=client(async(u,i)=>i?.method==='POST'?Promise.reject(Error('lost')):String(u).includes('/admissions/')?Response.json({request_id:'q',session_id:'child',state:'complete',reason:'',project_label:label}):Response.json({schema_version:1,session,cursor:0,runs:[],has_older:false}));
 const ops=new NewSessionOperations(c,{id:()=> 'q'});await assert.rejects(ops.create({mode:'default',mcp:true,catalog_project_id:'label',content:''}));await assert.rejects(ops.recover(),/MCP intent/);assert.equal(ops.locked,true);
});
test('unbounded acknowledgement or recovery session IDs never enter persisted recovery storage',async()=>{
 for(const source of ['ack','lookup']){const saved:string[]=[];const c=client(async(u,i)=>i?.method==='POST'?source==='ack'?Response.json({...session,id:'x'.repeat(129)}):Promise.reject(Error('lost')):Response.json({request_id:'q',session_id:'x'.repeat(129),state:'complete',reason:'',mcp_opt_in:false,project_label:label}));const ops=new NewSessionOperations(c,{id:()=> 'q',save:v=>{if(v)saved.push(v)}});await assert.rejects(ops.create({mode:'default',mcp:false,catalog_project_id:'label',content:''}));if(source==='lookup')await assert.rejects(ops.recover());assert.ok(saved.every(v=>!v.includes('x'.repeat(129))));assert.equal(ops.locked,true)}
});
test('visible label escaping distinguishes literal escape text from actual controls without changing identity',()=>{
 assert.notEqual(projectLabelText({...label,name:'\n'}),projectLabelText({...label,name:'\\u000a'}));assert.notEqual(projectLabelText({...label,project_id:'a\u202eb'}),projectLabelText({...label,project_id:'a\\u202eb'}));assert.equal(label.project_id,'label');
});
test('protocol preflight failures prove POST never invoked and allow only a new explicit attempt',async()=>{
 for(const failure of ['network','old404','malformed','null','incompatible']){
  let healthy=false,posts=0,gets=0,ids=0;let saved:string|null=null;
  const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 'secret',fetch:async(u,i)=>{
   if(i?.method==='POST'){posts++;return Response.json({...session,project_label:undefined})}
   gets++;if(healthy)return Response.json({protocol:protocolFlavor});
   if(failure==='network')throw Error('offline');if(failure==='old404')return new Response('',{status:404});if(failure==='malformed')return new Response('{');return Response.json(failure==='null'?null:{protocol:'old'});
  }});
  const ops=new NewSessionOperations(c,{id:()=> 'attempt-'+(++ids),save:v=>saved=v});const input={mode:'default' as const,mcp:false,catalog_project_id:'',content:'private draft'};
  await assert.rejects(ops.create(input));assert.equal(posts,0);assert.equal(gets,1);assert.equal(ops.locked,false,failure);assert.equal(saved,null);assert.equal(await ops.recover(),null);assert.equal(gets,1);
  healthy=true;assert.equal((await ops.create(input))?.id,'child');assert.equal(posts,1);assert.equal(ops.requestID,'attempt-2');
 }
 const c=client(async(u,i)=>{if(i?.method==='POST')throw Error('lost POST');return Response.json({error:{code:'admission_not_found'}},{status:404})});const ops=new NewSessionOperations(c);await assert.rejects(ops.create({mode:'default',mcp:false,catalog_project_id:'',content:''}));await assert.rejects(ops.recover());assert.equal(ops.phase,'not_found');assert.equal(ops.locked,true);
});
test('new idle recovery separates previous success from current request state and exact saved/displayed identity',async()=>{
 for(const state of ['starting','unresolved','complete','notfound']){
  let saved:string|null=null;let reads=0;
  const c=client(async(u,i)=>{if(i?.method==='POST')return Response.json(session);reads++;if(String(u).includes('/admissions/'))return state==='notfound'?Response.json({error:{code:'admission_not_found'}},{status:404}):Response.json({request_id:'second',session_id:'second-session',state,reason:'',mcp_opt_in:false,...(state==='complete'?{project_label:label}:{})});throw Error('snapshot offline')});
  const ops=new NewSessionOperations(c,{id:()=> 'first',save:v=>saved=v});await ops.create({mode:'default',mcp:false,catalog_project_id:'label',content:''});ops.cancel();ops.applied('child');
  if(state==='notfound'||state==='complete')await assert.rejects(ops.recover('second'));else await ops.recover('second');
  assert.equal(ops.result,null,state);assert.equal(ops.cancelRequested,false);assert.equal(ops.requestID,'second');assert.equal(ops.sessionID,state==='notfound'?'':'second-session');assert.equal(ops.phase,state==='complete'?'unknown':state==='notfound'?'not_found':state);assert.deepEqual(JSON.parse(saved!),state==='notfound'?{request_id:'second'}:{request_id:'second',session_id:'second-session'});assert.ok(!ops.recoveryHint.includes('child'));assert.equal(ops.admission?.request_id,state==='notfound'?undefined:'second');
 }
});

test('same completed request retains known result after apply despite failing storage and unavailable reads',async()=>{
 let reads=0;const c=client(async(u,i)=>{if(i?.method==='POST')return Response.json(session);reads++;throw Error('offline')});const ops=new NewSessionOperations(c,{id:()=> 'first',save:()=>{throw Error('unavailable')}});await ops.create({mode:'default',mcp:false,catalog_project_id:'label',content:''});ops.applied('child');assert.equal((await ops.recover('first'))?.id,'child');assert.equal(ops.requestID,'first');assert.equal(ops.sessionID,'child');assert.equal(ops.phase,'confirmed');assert.equal(reads,0);assert.match(ops.storageWarning,/unavailable/);
});

test('display JSON remains exact for astral controls, literal escapes and unloaded session',()=>{const raw='x\u{e0001}'+String.raw`\udb40\udc01`;const shown=displayJSON(raw);assert.doesNotMatch(shown,/\p{Cf}/u);assert.equal(JSON.parse(shown),raw);assert.equal(displayJSON(undefined),'undefined')});

test('same settled request retains admission evidence after explicit abandon and failed later lookup',async()=>{
 let missing=false;const c=client(async()=>missing?Response.json({error:{code:'admission_not_found'}},{status:404}):Response.json({request_id:'settled',session_id:'settled-session',state:'unresolved',mcp_opt_in:false,reason:''}));const ops=new NewSessionOperations(c);await ops.recover('settled');ops.cancel();ops.abandon();missing=true;await assert.rejects(ops.recover('settled'));assert.equal(ops.admission?.state,'unresolved');assert.equal(ops.sessionID,'settled-session');assert.equal(ops.cancelRequested,true);assert.notEqual(ops.phase,'not_found');
});

for(const mode of ['plan','default','acceptEdits'] as const) {
 test(`disabled native admission stays quarantined and recovers by GET only: ${mode}`,async()=>{
  const calls:{method:string;url:string;body?:unknown}[]=[];let saved:string|null=null;
  const c=client(async(u,i)=>{
   const method=i?.method??'GET';const url=String(u);calls.push({method,url,...(i?.body?{body:JSON.parse(String(i.body))}:{})});
   if(method==='POST')return Response.json({schema_version:1,error:{code:'workspace_unavailable_choose_chat_only'}},{status:409});
   assert.ok(url.endsWith('/admissions/native-original'));return Response.json({request_id:'native-original',session_id:'unpublished',state:'unresolved',mcp_opt_in:false,reason:'Single-use admission; inspect only.'});
  });
  const ops=new NewSessionOperations(c,{id:()=> 'native-original',save:v=>saved=v});
  const input={mode,model_id:'m',mcp:false,catalog_project_id:'',content:'private initial content'};
  await assert.rejects(ops.create(input),/workspace_unavailable_choose_chat_only/);
  assert.equal(ops.locked,true);assert.equal(ops.requestID,'native-original');assert.deepEqual(ops.pending,{...input,request_id:'native-original'});
  assert.deepEqual(calls[0]?.body,{mode,model_id:'m',request_id:'native-original'});
  assert.equal(await ops.create(input),null);await assert.rejects(ops.create({...input,mode:'chat_only'}),/different admission intent unresolved/);
  ops.cancel();assert.equal(ops.locked,true);assert.equal(await ops.recover(),null);assert.equal(ops.phase,'unresolved');assert.equal(ops.admission?.mcp_opt_in,false);
  assert.deepEqual(ops.pending,{...input,request_id:'native-original'});assert.equal(ops.requestID,'native-original');assert.equal(ops.result,null);
  const restored=new NewSessionOperations(c,{restore:saved});assert.equal(restored.locked,true);assert.equal(restored.requestID,'native-original');restored.cancel();assert.equal(await restored.recover(),null);assert.equal(restored.phase,'unresolved');assert.equal(restored.locked,true);
  assert.equal(calls.filter(v=>v.method==='POST').length,1);assert.equal(calls.filter(v=>v.method==='GET').length,2);
  assert.ok(calls.every(v=>!v.url.includes('/messages')&&!v.url.endsWith('/status')));assert.ok(!JSON.stringify(calls).includes('chat_only'));assert.ok(!JSON.stringify(calls).includes('private initial content'));
 });
}
