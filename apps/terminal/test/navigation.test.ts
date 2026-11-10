import {test} from 'node:test';
import assert from 'node:assert/strict';
import {AgentClient,ContextOperations,QueueOperations} from '@personal-agent/client';
import {protocolFlavor} from '@personal-agent/protocol';
import {resumeSession} from '../src/navigation.ts';

const item={id:'item',session_id:'source',request_id:'original-request',content:'original',state:'pending',run_id:'',position:1,phase:'',blocked_reason:'waiting_capacity',policy_digest:'policy'};
const record={schema_version:1,session_id:'source',request_id:'original-request',kind:'enqueue',item};
const snapshot={schema_version:1,session:{id:'target',model_id:'fixture',mode:'chat_only',files:false,tools:false,reason:''},cursor:0,runs:[],has_older:false};
function deferred<T>(){let resolve!:(v:T)=>void,reject!:(e:unknown)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
for(const phase of ['sending','unknown','applied'] as const)test(`Ink delayed resume stays at queue origin after ${phase} enqueue`,async()=>{
 const read=deferred<Response>(),write=deferred<Response>(),reading=deferred<void>(),writing=deferred<void>();let posts=0,ids=0;let selected='source';
 const client=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 'fixture',fetch:async(u,i)=>{
  if(String(u).endsWith('/status'))return Response.json({protocol:protocolFlavor});
  if(i?.method==='POST'){posts++;assert.equal(String(u).split('/').at(-2),'source');assert.deepEqual(JSON.parse(String(i.body)),{content:'original',request_id:'original-request'});writing.resolve();return write.promise}
  assert.ok(String(u).endsWith('/sessions/target/snapshot'));reading.resolve();return read.promise;
 }});
 const context=new ContextOperations(client),queue=new QueueOperations(client,{id:()=>{ids++;return 'original-request'}});
 const navigation=resumeSession(client,'target',context,queue,id=>{selected=id});await reading.promise;
 const enqueue=queue.enqueue('source','original');const outcome=enqueue.catch(error=>error);await writing.promise;
 if(phase==='unknown'){write.reject(new Error('lost acknowledgement'));assert.ok(await outcome instanceof Error);assert.equal(queue.phase,'unknown')}
 if(phase==='applied'){write.resolve(Response.json(record));await enqueue;queue.applied('source');assert.equal(queue.locked,false)}
 read.resolve(Response.json(snapshot));assert.equal(await navigation,false);assert.equal(selected,'source','old GET navigated away from original queued input');
 if(phase!=='applied'){assert.equal(queue.sourceSessionID,'source');assert.match(queue.recoveryHint,/original-request/);await queue.enqueue('source','original')}
 assert.equal(posts,1);assert.equal(ids,1);
 if(phase==='sending'){assert.equal(queue.phase,'sending');write.resolve(Response.json(record));await outcome}
});
test('Ink resume without intervening mutation selects only the inspected target',async()=>{
 let posts=0;const client=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 'fixture',fetch:async(_u,i)=>{if(i?.method==='POST')posts++;return Response.json(snapshot)}});let selected='source';
 assert.equal(await resumeSession(client,'target',new ContextOperations(client),new QueueOperations(client),id=>{selected=id}),true);assert.equal(selected,'target');assert.equal(posts,0);
});

import {JobOperations} from '@personal-agent/client';
for(const phase of ['sending','unknown','applied'] as const)test(`Ink delayed resume remains at immutable Job origin after ${phase}`,async()=>{
 const read=deferred<Response>(),write=deferred<Response>(),reading=deferred<void>(),writing=deferred<void>();let selected='source';let posts=0;
 const client=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 'fixture',fetch:async(u,i)=>{if(String(u).endsWith('/status'))return Response.json({protocol:protocolFlavor});if(i?.method==='POST'){posts++;writing.resolve();return write.promise}reading.resolve();return read.promise}});
 const jobs=new JobOperations(client,{id:()=> 'original-request'});const navigation=resumeSession(client,'target',new ContextOperations(client),new QueueOperations(client),id=>{selected=id},jobs);await reading.promise;const create=jobs.create('source','original',false);const outcome=create.catch(e=>e);await writing.promise;
 const result={schema_version:1,session_id:'source',request_id:'original-request',kind:'create',payload:'["original","",false]',job:{id:'job',origin_session_id:'source',origin_run_id:'',session_id:'execution',run_id:'run',request_id:'original-request',content:'original',effects:false,state:'registered',phase:'',blocked_reason:'',policy_digest:'a'.repeat(64)}};
 if(phase==='unknown'){write.reject(new Error('lost acknowledgement'));await outcome}if(phase==='applied'){write.resolve(Response.json(result));await create;jobs.applied('source')}
 read.resolve(Response.json(snapshot));assert.equal(await navigation,false);assert.equal(selected,'source');assert.equal(posts,1);if(phase==='sending'){write.resolve(Response.json(result));await outcome}
});

import {OperatorOperations} from '@personal-agent/client';
test('late Ink navigation remains fenced even after metadata cancellation resolves',async()=>{let selected='source';const pending=deferred<Response>();const c=new AgentClient({baseURL:'http://localhost:1',token:()=> 'fixture',fetch:async()=>pending.promise});const p={schema_version:1,namespace:'runtime-rebuild-v1',operation_id:'p',session_id:'source',scope_id:'',policy_digest:'a'.repeat(64),kind:'project.rename',target_id:'project',old_value:'old',new_value:'new',revision:'b'.repeat(64),expires_at_ms:Date.now()+100000,digest:'c'.repeat(64),state:'pending',decision:'',decision_request_id:'',reason:''};const actions=new OperatorOperations({prepareOperator:async()=>p,decideOperator:async()=>({...p,state:'rejected',decision:'cancel',decision_request_id:'d'}),operatorOperation:async()=>p},{id:(()=>{let n=0;return()=>n++?'d':'p'})()});const nav=resumeSession(c,'target',new ContextOperations(c),new QueueOperations(c),id=>selected=id,undefined,actions);await actions.prepare('source','project.rename','project','new');await actions.decide('cancel');assert.equal(actions.locked,false);pending.resolve(Response.json(snapshot));assert.equal(await nav,false);assert.equal(selected,'source')});

import {KnowledgeOperations} from '@personal-agent/client';
test('late Ink navigation remains fenced after a knowledge pending lookup resolves to cancellation',async()=>{let selected='source';const pending=deferred<Response>();const api=new AgentClient({baseURL:'http://localhost:1',token:()=> 'fixture',fetch:async()=>pending.promise});const hash='a'.repeat(64);const p={schema_version:1,namespace:'runtime-rebuild-v1',operation_id:'p',session_id:'source',scope_id:'scope',policy_digest:hash,kind:'ingest',document_id:'knowledge-'+hash,source_kind:'file',source:'note.txt',content_sha256:hash,content_bytes:5,excerpt:'hello',index_policy:'local-utf8-fields160-v1',expected_revision:'',expected_index_revision:'',result_revision:'',index_state:'staged',expires_at_ms:Date.now()+60000,digest:hash,state:'pending',decision:'',decision_request_id:'',reason:''};const actions=new KnowledgeOperations({prepareKnowledge:async()=>p,decideKnowledge:async()=>({...p,state:'rejected',index_state:'not_applied',decision:'cancel',decision_request_id:'d'}),knowledgeOperation:async()=>p},{id:()=> 'd'});const nav=resumeSession(api,'target',new ContextOperations(api),new QueueOperations(api),id=>selected=id,undefined,undefined,actions);await actions.recover('source','p');assert.equal(actions.locked,true);await actions.decide('cancel');pending.resolve(Response.json(snapshot));assert.equal(await nav,false);assert.equal(selected,'source')});

import {NewSessionOperations} from '@personal-agent/client';
for(const phase of ['sending','unknown','applied'] as const)test(`late Ink resume cannot replace a newer ${phase} admission`,async()=>{
 const read=deferred<Response>(),write=deferred<Response>();let selected='source';const c=new AgentClient({baseURL:'http://localhost:7319',token:()=> 'owned',fetch:async(u,i)=>String(u).endsWith('/protocol')?Response.json({protocol:protocolFlavor}):i?.method==='POST'?write.promise:read.promise});
 const context=new ContextOperations(c),queue=new QueueOperations(c),admission=new NewSessionOperations(c);const navigation=resumeSession(c,'target',context,queue,id=>selected=id,undefined,undefined,undefined,admission);
 const creating=admission.create({mode:'chat_only',mcp:false,catalog_project_id:'',content:''});const result=creating.catch(e=>e);
 if(phase==='unknown'){write.reject(Error('lost'));await result}if(phase==='applied'){write.resolve(Response.json({...snapshot.session,id:'new'}));await result;admission.applied('new')}
 read.resolve(Response.json(snapshot));assert.equal(await navigation,false);assert.equal(selected,'source');if(phase==='sending'){write.reject(Error('lost'));await result}
});
