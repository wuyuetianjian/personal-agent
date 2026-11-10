import {protocolFlavor} from '@personal-agent/protocol';
import {test} from 'node:test';import assert from 'node:assert/strict';
import {AgentClient} from '@personal-agent/client';
import {admitCLI,recoverAdmissionCLI} from '../src/new_session_cli.ts';
const session={id:'s',model_id:'m',mode:'chat_only',files:false,tools:false,reason:''};
test('CLI lost admission returns pure static JSON and GET recovery, never submits initial content',async()=>{
 const calls:string[]=[];let out='';const c=new AgentClient({baseURL:'http://localhost:7319',token:()=> 'x',fetch:async(u,i)=>{if(String(u).endsWith('/protocol'))return Response.json({protocol:protocolFlavor});calls.push(i?.method??'GET');if(i?.method==='POST')throw Error('secret hostile response');if(String(u).includes('/admissions/'))return Response.json({request_id:'q',session_id:'s',state:'complete',mcp_opt_in:false,reason:''});return Response.json({schema_version:1,session,cursor:0,runs:[],has_older:false})}});
 const result=await admitCLI(c,{mode:'chat_only',mcp:false,catalog_project_id:'',content:'private'},'q',s=>out+=s);assert.equal(result.exitCode,4);assert.equal(result.session,undefined);const v=JSON.parse(out);assert.equal(v.request_id,'q');assert.equal(v.session_id,'s');assert.equal(v.status,'admission_recovered');assert.match(v.recovery_command,/--admission/);assert.match(v.recovery_command,/http:\/\/localhost:7319/);assert.ok(!out.includes('private')&&!out.includes('hostile'));assert.deepEqual(calls,['POST','GET','GET']);out='';assert.equal(await recoverAdmissionCLI(c,'q',s=>out+=s),0);assert.equal(JSON.parse(out).session.id,'s');assert.match(JSON.parse(out).initial_content,/not sent by this command/);assert.equal(calls.filter(x=>x==='POST').length,1);
});
test('CLI successful creation alone leaves content to one existing print path; preclaim vs provider failure remain distinct',async()=>{
 for(const code of ['ok','project_label_unavailable','provider_unavailable']){let out='';const c=new AgentClient({baseURL:'http://localhost:7319',token:()=> 'x',fetch:async(u,i)=>String(u).endsWith('/protocol')?Response.json({protocol:protocolFlavor}):i?.method==='POST'?code==='ok'?Response.json(session):Response.json({error:{code}},{status:409}):Response.json({error:{code:'admission_not_found'}},{status:404})});const r=await admitCLI(c,{mode:'chat_only',mcp:false,catalog_project_id:'',content:'initial'},'q',s=>out+=s);if(code==='ok'){assert.equal(r.session?.id,'s');assert.equal(out,'')}else{assert.equal(r.exitCode,code==='project_label_unavailable'?2:4);assert.equal(JSON.parse(out).reason,code==='project_label_unavailable'?'admission_refused_before_claim':'admission_unconfirmed')}}
});

import {spawnSync} from 'node:child_process';
test('empty admission selector cannot fall through to a new session/prompt',()=>{
 const r=spawnSync('node',['--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--admission','','--print','unexpected','--format','json'],{encoding:'utf8',env:{...process.env,PA_AGENT_TOKEN:''}});assert.equal(r.status,2);assert.match(r.stderr,/--admission/);assert.equal(r.stdout,'');
});
test('CLI preflight never-sent failure is rejected without admission lookup or automatic retry',async()=>{
 for(const failure of ['network','old404','malformed']){const calls:string[]=[];let out='';const c=new AgentClient({baseURL:'http://localhost:7319',token:()=> 'x',fetch:async(u,i)=>{calls.push((i?.method??'GET')+' '+u);if(failure==='network')throw Error('offline');return new Response(failure==='malformed'?'{':'',{status:failure==='old404'?404:200})}});const result=await admitCLI(c,{mode:'chat_only',mcp:false,catalog_project_id:'',content:'private'},'q',s=>out+=s);assert.equal(result.exitCode,2);assert.equal(JSON.parse(out).status,'rejected');assert.equal(JSON.parse(out).initial_content,'not sent');assert.deepEqual(calls,['GET http://localhost:7319/api/v1/protocol'])}
});
