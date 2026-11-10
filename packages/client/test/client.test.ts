import {test} from 'node:test';
import assert from 'node:assert/strict';
import { AgentClient as RealAgentClient, APIError, safeTerminalText } from '../src/index.ts';
import type {ClientOptions} from '../src/index.ts';
import {protocolFlavor} from '@personal-agent/protocol';
// Server-side fixture handshake; all tested client behavior remains the real SDK.
class AgentClient extends RealAgentClient {constructor(options:ClientOptions){super({...options,fetch:(u,i)=>(String(u).endsWith('/status')||String(u).endsWith('/protocol'))?Promise.resolve(Response.json({protocol:protocolFlavor})):options.fetch!(u,i)})}}
test('writes require explicit IDs, bearer in header only, unknown result is never retried',async()=>{
 let calls=0;let saved:RequestInit|undefined;let url='';
 const client=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 'private',fetch:async(u,i)=>{calls++;saved=i;url=String(u);throw new Error('connection lost')}});
 await assert.rejects(client.submitMessage('s','hello','q'),/connection lost/);assert.equal(calls,1);assert.equal(new Headers(saved?.headers).get('Authorization'),'Bearer private');assert.ok(!url.includes('private'));assert.match(String(saved?.body),/"request_id":"q"/);
 await assert.rejects(client.submitMessage('s','hello',''),/request ID/);assert.equal(calls,1);
});
test('snapshot plus UTF8 fragmented SSE, gap fetches new snapshot without any POST',async()=>{
 const snapshot={schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''},cursor:0,runs:[],has_older:false};
 let snapshots=0;const methods:string[]=[];let eventCalls=0;const ac=new AbortController();
 const client=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(u,i)=>{methods.push(i?.method??'GET');if(String(u).includes('/snapshot')){snapshots++;return Response.json({...snapshot,cursor:snapshots===1?0:3})}eventCalls++;const e={schema_version:1,session_id:'s',run_id:'r',sequence:eventCalls===1?1:5,type:'run.created',data:{request_id:'q',input:'你好',state:'running'}};const bytes=new TextEncoder().encode(`event: event\r\ndata: ${JSON.stringify(e)}\r\n\r\n`);return new Response(new ReadableStream({start(c){for(const byte of bytes)c.enqueue(Uint8Array.of(byte));c.close()}}),{headers:{'content-type':'text/event-stream'}})}});
 const frames=[];for await(const frame of client.watchSession('s',{signal:ac.signal,reconnectDelayMs:0})){frames.push(frame);if(frames.length===3){ac.abort();break}}
 assert.equal(frames[1]?.kind,'event');assert.equal(frames[2]?.kind,'snapshot');assert.equal(snapshots,2);assert.ok(methods.every(m=>m==='GET'));
});
test('API errors remain structured and terminal rendering removes hostile escapes',async()=>{
 const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async()=>Response.json({schema_version:1,error:{code:'provider_changed',message:'provider_changed'}},{status:409})});
 await assert.rejects(c.cancelRun('r','q'),e=>e instanceof APIError&&e.code==='provider_changed');
 assert.equal(safeTerminalText('\u001b]52;c;c2VjcmV0\u0007你好\u001b[31m!\u001b[0m\u0000'),'你好!');
});
test('abrupt body failure reconnects with last cursor, without replaying writes',async()=>{
 let calls=0;const urls:string[]=[];const snapshot={schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''},cursor:0,runs:[],has_older:false};
 const event=(n:number)=>new TextEncoder().encode(`event: event\ndata: ${JSON.stringify({schema_version:1,session_id:'s',run_id:'r',sequence:n,type:'future',data:{}})}\n\n`);
 const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(u)=>{urls.push(String(u));if(String(u).includes('/snapshot'))return Response.json(snapshot);calls++;if(calls===1){let delivered=false;return new Response(new ReadableStream({pull(controller){if(!delivered){delivered=true;controller.enqueue(event(1))}else controller.error(new Error('socket reset'))}}),{headers:{'content-type':'text/event-stream'}})}return new Response(event(2),{headers:{'content-type':'text/event-stream'}})}});
 const frames=[];for await(const f of c.watchSession('s',{reconnectDelayMs:0})){frames.push(f);if(frames.length===3)break}assert.equal(calls,2);assert.match(urls.at(-1)??'',/after=1$/);
});
test('truncated UTF8/frame retries only boundedly; malformed protocol and auth fail immediately',async()=>{
 const snapshot={schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''},cursor:0,runs:[],has_older:false};
 for(const bytes of [Uint8Array.of(0xe4,0xbd),new TextEncoder().encode('event: event\ndata: {')]){let calls=0;const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(u)=>{if(String(u).includes('/snapshot'))return Response.json(snapshot);calls++;return new Response(bytes,{headers:{'content-type':'text/event-stream'}})}});await assert.rejects(async()=>{for await(const _ of c.watchSession('s',{reconnectDelayMs:0,maxReconnects:1})){/* read */}},/incomplete|UTF|reconnect/);assert.equal(calls,2)}
 for(const response of [()=>new Response('event: event\ndata: {}\n\n',{headers:{'content-type':'text/event-stream'}}),()=>Response.json({error:{code:'unauthorized'}},{status:401})]){let calls=0;const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(u)=>{if(String(u).includes('/snapshot'))return Response.json(snapshot);calls++;return response()}});await assert.rejects(async()=>{for await(const _ of c.watchSession('s',{reconnectDelayMs:0})){/* read */}});assert.equal(calls,1)}
});
test('abort cancels an idle stream reader and releases watcher', {timeout:2000},async()=>{
 let cancelled=false;const ac=new AbortController();const snapshot={schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''},cursor:0,runs:[],has_older:false};let entered!:()=>void;const started=new Promise<void>(r=>{entered=r});
 const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(u)=>{if(String(u).includes('/snapshot'))return Response.json(snapshot);entered();return new Response(new ReadableStream({cancel(){cancelled=true}}),{headers:{'content-type':'text/event-stream'}})}});
 const watcher=c.watchSession('s',{signal:ac.signal});await watcher.next();const pending=watcher.next();await started;await new Promise(r=>setTimeout(r,0));ac.abort();assert.equal((await pending).done,true);assert.equal(cancelled,true);
});

test('old API-major1 server is rejected by handshake before any POST',async()=>{const methods:string[]=[];const c=new RealAgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(_u,i)=>{methods.push(i?.method??'GET');return Response.json({schema_version:1})}});await assert.rejects(c.createSession('chat_only','q'),/incompatible_protocol/);assert.deepEqual(methods,['GET'])});

test('approval reviews reject unknown/mixed arms instead of trusting a TypeScript assertion',async()=>{
 for(const review of [{kind:'unknown'},{kind:'exec',exec:{}},{kind:'edit',edit:{scope:'s',path:'p',old_content:'o',new_content:'n',old_sha256:'a',new_sha256:'b'},exec:{}}]){
  const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async()=>Response.json(review)});
  await assert.rejects(c.approvalReview('a'),/review/);
 }
});

test('snapshot and history reject malformed optional fields and wrong session', async()=>{
 const base={schema_version:1,session:{id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''},cursor:3,runs:[{id:'r',request_id:'q',input:'x',state:'running',phase:'model',text:'',draft:'',tools:[{id:'t',name:'n',arguments:'{}',state:'completed'}]}],has_older:true};
 for(const patch of [{next_before:'bad'}, {session:{...base.session,id:'other'}}, {schema_version:2}, {runs:[{...base.runs[0],tools:[{...base.runs[0]!.tools[0],result:42}]}]}, {runs:[{...base.runs[0],tools:[{...base.runs[0]!.tools[0],is_error:'false'}]}]}]){
  const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async()=>Response.json({...base,...patch})});
  await assert.rejects(c.snapshot('s')); await assert.rejects(c.history('s'));
 }
});
test('invalid model usage is rejected at protocol boundary',async()=>{
 const {parseFrame}=await import('@personal-agent/protocol');
 for(const usage of [{InputTokens:'1',OutputTokens:2},{InputTokens:-1,OutputTokens:2}, {InputTokens:1}]) assert.throws(()=>parseFrame('event',JSON.stringify({schema_version:1,session_id:'s',run_id:'r',sequence:1,type:'model.completed',data:{text:'x',usage}})));
});
test('default browser fetch retains global receiver',async()=>{
 const original=globalThis.fetch;
 try{globalThis.fetch=async function(this:unknown){if(this!==globalThis)throw new TypeError('Illegal invocation');return Response.json({protocol:protocolFlavor})};const c=new RealAgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't'});await c.connect()}finally{globalThis.fetch=original}
});
test('cancel signal aborts an idle write without retry',async()=>{let calls=0;const ac=new AbortController();const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(_u,init)=>{calls++;return await new Promise<Response>((_resolve,reject)=>init?.signal?.addEventListener('abort',()=>reject(new Error('aborted')),{once:true}))}});const p=c.cancelRun('r','q',ac.signal);await new Promise(r=>setTimeout(r,0));ac.abort();await assert.rejects(p,/aborted/);assert.equal(calls,1)});
test('session operations send explicit boundary cursor and ID without write retry',async()=>{
 const calls:{url:string;body:Record<string,unknown>}[]=[];
 const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(u,i)=>{calls.push({url:String(u),body:JSON.parse(String(i?.body))});throw new Error('lost response')}});
 await assert.rejects(c.branchSession('s','r',7,'b'),/lost response/);
 await assert.rejects(c.compactSession('s',7,'c','original'),/lost response/);
 await assert.rejects(c.clearSession('s',7,'d'),/lost response/);
 await assert.rejects(c.rewindSession('s','r',7,'e'),/lost response/);
 assert.equal(calls.length,4);assert.deepEqual(calls[0]?.body,{boundary_run_id:'r',expected_cursor:7,request_id:'b'});assert.equal(calls[1]?.body.selection,'original');assert.ok(calls[3]?.url.endsWith('/rewind'));
});
test('context views reject cross-session and malformed lower-trust data',async()=>{
 for(const view of [{schema_version:2},{schema_version:1,selection:'compacted',warning:'x',original:[],selected:[],version:{id:'v',session_id:'foreign',source_cursor:0,method:'local-omit-resolved-v1',omitted_messages:0}}]){
 const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async()=>Response.json(view)});await assert.rejects(c.contextView('s'));
 }
});
test('original context view still verifies its owning session',async()=>{
 const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async()=>Response.json({schema_version:1,session_id:'other',selection:'original',warning:'x',original:[],selected:[]})});await assert.rejects(c.contextView('s'),/session/);
});

test('session HTTP acknowledgements and list reject corrupt project snapshots without POST replay',async()=>{
 const session={id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:'',project_label:{project_id:'p',generation:'a'.repeat(32),revision:1,name:'exact',scope_id:'forged'}};
 let posts=0;
 const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(_u,i)=>{if(i?.method==='POST')posts++;return Response.json(i?.method==='POST'?session:[session])}});
 await assert.rejects(c.createSession('chat_only','q'),/project label/);
 await assert.rejects(c.clearSession('s',0,'c'),/project label/);
 await assert.rejects(c.branchSession('s','r',0,'b'),/project label/);
 await assert.rejects(c.rewindSession('s','r',0,'w'),/project label/);
 await assert.rejects(c.listSessions(),/project label/);
 assert.equal(posts,4);
});

test('admission recovery is strict read-only and retains immutable optional label',async()=>{
 const value={request_id:'owned/request',session_id:'s',state:'complete',reason:'read only',project_label:{project_id:'p',generation:'a'.repeat(32),revision:1,name:'snapshot'}};
 const requests:{url:string;method:string}[]=[];
 const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async(u,i)=>{requests.push({url:String(u),method:i?.method??'GET'});return Response.json(value)}});
 assert.deepEqual(await c.admission('owned/request'),value);
 assert.deepEqual(requests,[{url:'http://127.0.0.1:7319/api/v1/admissions/owned%2Frequest',method:'GET'}]);
 for(const patch of [{request_id:'foreign'},{state:'invented'},{state:'starting'},{project_label:{...value.project_label,revision:0}},{project_label:null},{scope_id:'forged'}]) {
  const c=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async()=>Response.json({...value,...patch})});await assert.rejects(c.admission('owned/request'));
 }
});
