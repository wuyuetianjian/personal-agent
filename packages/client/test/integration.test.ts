import {fixtureName,readFixtureInfo,stopFixture} from './fixture_process.ts';
import {test} from 'node:test';import assert from 'node:assert/strict';import {spawn,spawnSync} from 'node:child_process';import {mkdtemp,readFile,rm} from 'node:fs/promises';import {tmpdir} from 'node:os';import {join} from 'node:path';
import {AgentClient} from '../src/index.ts';import {emptyState,reduceSession} from '@personal-agent/protocol';
test('real Go server: streamed model → exact approval → native tool → answer; reload and cancel', {timeout:60000},async()=>{
 const dir=await mkdtemp(join(tmpdir(),'agent-sdk-'));const binary=join(dir,fixtureName());const build=spawnSync(process.env['GO_BINARY']??'go',['build','-o',binary,'./cmd/agent-fixture'],{encoding:'utf8'});assert.equal(build.status,0,build.stderr);
 const child=spawn(binary,[],{env:{...process.env,PA_AGENT_TOKEN:'sdk-test-token'},stdio:['ignore','pipe','pipe']});
 try{const info=await readFixtureInfo<{url:string;workspace:string}>(child);const client=new AgentClient({baseURL:info.url,token:()=> 'sdk-test-token'});
 const session=await client.createSession(process.platform==='linux'?'default':'chat_only','create');let state=emptyState(session.id);const controller=new AbortController();let sawDelta=false;let approved=false;let completed=false;const watch=client.watchSession(session.id,{signal:controller.signal});await watch.next();await client.submitMessage(session.id,process.platform==='linux'?'edit':'你好','message');
 for await(const frame of watch){state=reduceSession(state,frame);if(frame.kind==='event'&&frame.event.type==='model.text_delta')sawDelta=true;const run=state.runs.at(-1);if(run?.approval&&!approved){const review=await client.approvalReview(run.approval.id);assert.equal(review.kind,'edit');if(review.kind!=='edit')throw new Error('wrong review');assert.equal(review.edit.old_content,'old\n');assert.equal(review.edit.new_content,'new 你好\n');assert.equal(review.edit.path,'note.txt');approved=true;await client.decideApproval(run.approval.id,run.approval.digest,'approved','approve')};if(run?.state==='completed'){completed=true;controller.abort();break}}
 assert.ok(sawDelta);assert.ok(completed);if(process.platform==='linux'){assert.ok(approved);assert.equal(await readFile(join(info.workspace,'note.txt'),'utf8'),'new 你好\n')}
 const reloaded=await client.snapshot(session.id);assert.equal(reloaded.runs.length,1);assert.equal(reloaded.runs[0]?.state,'completed');
 const pending=await client.submitMessage(session.id,'wait','waiting');await client.cancelRun(pending.run_id,'cancel');
 for(let i=0;i<200;i++){const snap=await client.snapshot(session.id);if(snap.runs.at(-1)?.state==='cancelled'){completed=true;break}completed=false;await new Promise(r=>setTimeout(r,10))}assert.ok(completed,'cancel drain did not terminalize');
 }finally{await stopFixture(child);await rm(dir,{recursive:true,force:true})}
});
