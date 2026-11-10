import {test} from 'node:test';import assert from 'node:assert/strict';import {spawn,spawnSync} from 'node:child_process';import {mkdtemp,readFile,rm} from 'node:fs/promises';import {tmpdir} from 'node:os';import {join} from 'node:path';import {AgentClient} from '../src/index.ts';
test('real SDK/Go/stdio MCP: exact review and unknown Tasks never resume',{timeout:60000},async()=>{
 const dir=await mkdtemp(join(tmpdir(),'mcp-sdk-'));const bin=join(dir,'fixture');const build=spawnSync('go',['build','-o',bin,'./cmd/agent-fixture'],{encoding:'utf8'});assert.equal(build.status,0,build.stderr);
 const child=spawn(bin,['--safe-mcp','--mcp-result','task'],{env:{...process.env,PA_AGENT_TOKEN:'mcp-sdk-token'},stdio:['ignore','pipe','pipe']});
 try {let line='';for await(const chunk of child.stdout){line+=String(chunk);if(line.includes('\n'))break}const info=JSON.parse(line) as {url:string;workspace:string};const client=new AgentClient({baseURL:info.url,token:()=> 'mcp-sdk-token'});
  await client.status();await assert.rejects(readFile(join(info.workspace,'mcp-marker.txt.launch')),{code:'ENOENT'});
  const session=await client.createSession('default','new',undefined,true);const status=await client.mcp(session.id);assert.equal(status.servers[0]?.state,'discovered');await assert.rejects(readFile(join(info.workspace,'mcp-marker.txt')),{code:'ENOENT'});
  const run=await client.submitMessage(session.id,'mcp','call');let snap=await client.snapshot(session.id);
  for(let i=0;i<150&&!snap.runs[0]?.approval;i++){await new Promise(r=>setTimeout(r,20));snap=await client.snapshot(session.id)}
  const a=snap.runs[0]?.approval;assert.ok(a);const review=await client.approvalReview(a.id);assert.equal(review.kind,'mcp');if(review.kind!=='mcp')throw new Error('wrong arm');assert.equal(review.mcp.server_id,'owned-fixture');assert.equal(review.mcp.tool,'write');assert.equal(review.mcp.arguments,'{"value":"hello 世界"}');assert.match(review.mcp.schema_digest,/^[0-9a-f]{64}$/);
  await client.decideApproval(a.id,a.digest,'approved','approval');await client.decideApproval(a.id,a.digest,'approved','approval');
  for(let i=0;i<150&&snap.runs[0]?.phase!=='needs_reconciliation';i++){await new Promise(r=>setTimeout(r,20));snap=await client.snapshot(session.id)}
  assert.equal(snap.runs[0]?.phase,'needs_reconciliation');assert.equal(snap.runs[0]?.tools[0]?.state,'unknown');assert.match(snap.runs[0]?.tools[0]?.result??'',/unknown_requires_reconciliation/);
  await client.resumeRun(run.run_id,'attempt-resume');await new Promise(r=>setTimeout(r,100));const after=await client.snapshot(session.id);assert.equal(after.runs[0]?.phase,'needs_reconciliation');assert.equal(after.runs[0]?.tools.length,1);assert.equal((await readFile(join(info.workspace,'mcp-marker.txt'),'utf8')).trim().split('\n').length,1);
 }finally {if(child.exitCode===null){await new Promise<void>(resolve=>{const timer=setTimeout(()=>child.kill('SIGKILL'),10000);child.once('exit',()=>{clearTimeout(timer);resolve()});child.kill('SIGINT')})}await rm(dir,{recursive:true,force:true})}
});
