import {fixtureName,readFixtureInfo,stopFixture} from '../../../packages/client/test/fixture_process.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {spawn,spawnSync} from 'node:child_process';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {buildHookCLI,createHookCLIDirectory} from './build_hooks_cli.mjs';

test('actual compiled CLI disable loss and re-enable never revive an owned queued Skill run',{timeout:90000},async(t)=>{
 const dir=await mkdtemp(join(tmpdir(),'hooks-queued-cli-'));
 const env={...process.env,PA_AGENT_TOKEN:'queued-hook-fixture'};
 let server:ReturnType<typeof spawn>|undefined;
 let proxy:ReturnType<typeof createServer>|undefined;
 let cliDir:string|undefined;
 try{
  cliDir=createHookCLIDirectory();
  const binary=join(dir,fixtureName()),cli=join(cliDir,'cli.mjs');
  const build=spawnSync(process.env['GO_BINARY']??'go',['build','-o',binary,'./cmd/agent-fixture'],{encoding:'utf8',timeout:60000});
  assert.equal(build.status,0,build.stderr);
  const external=spawnSync(binary,['--queued-hook-run','--workspace',dir],{env,encoding:'utf8',timeout:5000});
  assert.equal(external.status,1);assert.match(external.stderr,/requires an exclusive owned temporary workspace/);
  buildHookCLI(cli);
  server=spawn(binary,['--queued-hook-run'],{env,stdio:['ignore','pipe','pipe']});
  const info=await readFixtureInfo<{url:string;runtime_parent:string;hook_session_id:string;hook_run_id:string;hook_revision_id:string}>(server);
  const calls:{method:string;path:string}[]=[];
  let dropDecision=false;
  proxy=createServer(async(req,res)=>{
   try{
    const chunks:Buffer[]=[];for await(const chunk of req)chunks.push(Buffer.from(chunk));
    const method=req.method!,path=req.url!;calls.push({method,path});
    const response=await fetch(info.url+path,{method,headers:{Authorization:'Bearer '+env.PA_AGENT_TOKEN,'X-Agent-Protocol':'personal-agent-rebuild-v1','Content-Type':'application/json'},...(method==='POST'?{body:Buffer.concat(chunks)}:{})});
    const body=await response.text();
    if(dropDecision&&method==='POST'&&path.endsWith('/hooks/decision')){dropDecision=false;res.destroy();return}
    res.writeHead(response.status,{'Content-Type':'application/json'});res.end(body);
   }catch{res.destroy()}
  });
  proxy.listen(0,'127.0.0.1');await once(proxy,'listening');
  const address=proxy.address();assert.ok(address&&typeof address!=='string');
  const url='http://127.0.0.1:'+address.port;
  const invoke=async(args:string[],expected=0)=>{
   const child=spawn('node',[cli,...args],{env,stdio:['ignore','pipe','pipe']});
   let out='',err='';child.stdout!.on('data',chunk=>out+=String(chunk));child.stderr!.on('data',chunk=>err+=String(chunk));
   const timer=setTimeout(()=>child.kill('SIGKILL'),15000);
   const [code]=await once(child,'exit');clearTimeout(timer);
   assert.equal(code,expected,out+err);
   assert.doesNotMatch(out,/queued-hook-fixture/);
   return JSON.parse(out);
  };
  const command=(args:string[],expected=0)=>invoke(['--url',url,'--session',info.hook_session_id,'--format','json',...args],expected);
  const show=()=>command(['--skill-run','show','--run-id',info.hook_run_id]);
  const counters=()=>{
   const code=`import sqlite3,json,sys,pathlib\nc=sqlite3.connect(next(pathlib.Path(sys.argv[1]).rglob('agent.db')))\nr=sys.argv[2]\nprint(json.dumps({'journal':c.execute('SELECT count(*) FROM side_effect_records WHERE workflow_id=?',(r,)).fetchone()[0],'native':c.execute('SELECT count(*) FROM interaction_tool_proposals WHERE run_id=? AND executed=1',(r,)).fetchone()[0],'steps':json.loads(c.execute('SELECT checkpoint FROM interaction_engine WHERE run_id=?',(r,)).fetchone()[0])['Steps'],'receipts':c.execute('SELECT count(*) FROM interaction_skill_hook_receipts WHERE run_id=?',(r,)).fetchone()[0]}))`;
   const result=spawnSync('python3',['-c',code,info.runtime_parent,info.hook_run_id],{encoding:'utf8'});
   assert.equal(result.status,0,result.stderr);return JSON.parse(result.stdout);
  };
  const queued=await show();assert.equal(queued.schema_version,2);assert.equal(queued.execution,'queued');assert.equal(queued.revoked,false);
  assert.deepEqual(counters(),{journal:0,native:0,steps:0,receipts:0});
  const preview=await command(['--hooks','disable','--request-id','disable-p']);
  assert.equal(preview.state,'pending');dropDecision=true;
  const unknown=await command(['--hooks','confirm','--operation-id',preview.operation_id,'--digest',preview.digest,'--request-id','disable-d'],2);
  assert.equal(unknown.state,'unknown');assert.equal(unknown.recovery_request_id,'disable-d');
  const before=calls.length;
  const recovered=await invoke(unknown.recovery_args);assert.equal(recovered.reason,'disabled');
  assert.deepEqual(calls.slice(before),[{method:'GET',path:'/api/v1/sessions/'+info.hook_session_id+'/hooks/operations/disable-d'}]);
  const disabled=await show();assert.equal(disabled.revoked,true);assert.equal(disabled.execution,'queued');
  assert.deepEqual(counters(),{journal:0,native:0,steps:0,receipts:0});
  const enable=await command(['--hooks','enable','--revision',info.hook_revision_id,'--request-id','reenable-p']);
  assert.equal((await command(['--hooks','confirm','--operation-id',enable.operation_id,'--digest',enable.digest,'--request-id','reenable-d'])).reason,'enabled');
  assert.equal((await command(['--hooks','list'])).generation,3);
  const still=await show();assert.equal(still.revoked,true);assert.equal(still.execution,'queued');assert.deepEqual(still.pin,queued.pin);
  assert.deepEqual(counters(),{journal:0,native:0,steps:0,receipts:0});
  // Only this explicit action asks the existing owner to finish the revoked run.
  await command(['--skill-run','resume','--run-id',info.hook_run_id,'--request-id','explicit-resume']);
  let finished=await show();for(let i=0;i<20&&finished.execution!=='revoked';i++){await new Promise(r=>setTimeout(r,50));finished=await show()}
  assert.equal(finished.execution,'revoked');assert.equal(finished.revoked,true);
  assert.ok(finished.nodes.every((node:{outcome:string;result:unknown})=>node.outcome==='not_started'&&node.result===null));
  const stable=counters();assert.deepEqual(stable,{journal:0,native:0,steps:0,receipts:1});
  const reads=calls.length;
  for(let i=0;i<3;i++){
   assert.equal((await command(['--skill-run','lookup','--run-id',info.hook_run_id,'--control','resume','--request-id','explicit-resume'])).run_id,info.hook_run_id);
   assert.equal((await show()).execution,'revoked');
   const receipts=await command(['--hooks','receipts','--run-id',info.hook_run_id]);assert.equal(receipts.receipts.length,1);
  }
  assert.ok(calls.slice(reads).every(call=>call.method==='GET'));
  assert.deepEqual(counters(),stable);
  assert.equal(calls.filter(call=>call.method==='POST'&&call.path.endsWith('/hooks/decision')).length,2);
  assert.equal(calls.filter(call=>call.method==='POST'&&call.path.endsWith('/resume')).length,1);
  t.diagnostic(JSON.stringify({initial:'queued',disabled:'revoked, queued',reenabled:'still revoked, queued',explicit_resume:finished.execution,generation:3,final_counts:stable,hook_decision_posts:2,resume_posts:1,repeated_recovery:'GET only'}));
 }finally{
  if(proxy){proxy.closeAllConnections();await new Promise<void>(resolve=>proxy!.close(()=>resolve()))}
  await stopFixture(server);
  if(cliDir)await rm(cliDir,{recursive:true,force:true});
  await rm(dir,{recursive:true,force:true});
 }
});
