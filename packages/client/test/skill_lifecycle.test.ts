import {fixtureName,readFixtureInfo,stopFixture} from './fixture_process.ts';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {spawn,spawnSync} from 'node:child_process';
import type {ChildProcessWithoutNullStreams} from 'node:child_process';
import {mkdtemp,mkdir,readFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {AgentClient,SkillOperations} from '../src/index.ts';

test('real Go/SDK skill lifecycle survives lost prepare and decision across daemon restarts with GET-only recovery',{timeout:90000},async()=>{
 const dir=await mkdtemp(join(tmpdir(),'skill-sdk-')),bin=join(dir,fixtureName()),runtime=join(dir,'runtime'),workspace=join(dir,'workspace');
 await mkdir(runtime);await mkdir(workspace);
 const build=spawnSync(process.env['GO_BINARY']??'go',['build','-o',bin,'./cmd/agent-fixture'],{encoding:'utf8',timeout:60000});assert.equal(build.status,0,build.stderr);
 let child:ChildProcessWithoutNullStreams|undefined;const token='owned-skill-fixture';let saved:string|null=null;let previewPosts=0,decisionPosts=0;let drop='preview';let lifecycle=false;const paths:string[]=[];
 async function stop(){await stopFixture(child);child=undefined}
 async function start(){child=spawn(bin,['--runtime-parent',runtime,'--workspace',workspace],{env:{...process.env,PA_AGENT_TOKEN:token},stdio:'pipe'});const info=await readFixtureInfo(child);
 return new AgentClient({baseURL:info.url,token:()=>token,fetch:async(url,init)=>{const path=new URL(String(url)).pathname;if(lifecycle){paths.push(path);assert.notEqual(path,'/api/v1/status')}if(path.endsWith('/skills/preview'))previewPosts++;if(path.endsWith('/skills/decision'))decisionPosts++;const response=await fetch(url,init);if(drop&&path.endsWith('/skills/'+drop)){assert.equal(response.status,200);await response.text();drop='';throw Error('fixture drops acknowledged response')}return response}})
 }
 try{
  let client=await start();const session=await client.createSession('chat_only','skill-session');lifecycle=true;
  const f=JSON.parse(await readFile(new URL('../../../protocol/v1/skills.golden.json',import.meta.url),'utf8'));const input={kind:'import',manifest_text:f.manifest_text,catalog_id:'',revision_id:''};
  assert.equal((await client.validateSkill(session.id,input.manifest_text)).evaluation,'not_run');
  const first=new SkillOperations(client,{id:()=> 'prepare-original',save:v=>{saved=v}});await assert.rejects(first.prepare(session.id,input),/fixture/);assert.equal(first.phase,'unknown');assert.equal((await client.listSkills(session.id)).items.length,0);assert.ok(saved);assert.doesNotMatch(saved,/lookup|manifest|owned-skill/);
  await stop();client=await start();const restored=new SkillOperations(client,{restore:saved,id:()=> 'decision-original',save:v=>{saved=v}});const recoveryStart=paths.length;await restored.recover();assert.equal(restored.phase,'pending');assert.ok(paths.slice(recoveryStart).every(p=>p.endsWith('/skills/operations/prepare-original')));assert.equal(previewPosts,1);
  drop='decision';await assert.rejects(restored.decide('confirm'),/fixture/);assert.equal(restored.phase,'unknown');assert.equal(await restored.decide('confirm'),null);assert.ok(String(saved).includes('decision-original'));
  await stop();client=await start();const final=new SkillOperations(client,{restore:saved,save:v=>{saved=v}});await final.recover();assert.equal(final.result?.state,'committed');assert.equal(final.phase,'resolved');assert.equal(previewPosts,1);assert.equal(decisionPosts,1);assert.equal(saved,null);
  const result=final.result!;const page=await client.listSkills(session.id);assert.equal(page.items.length,1);assert.equal(page.items[0]!.state,'disabled');assert.equal(page.items[0]!.selected_revision,'');
  const versions=await client.skillVersions(session.id,result.catalog_id);assert.equal(versions.items.length,1);assert.equal(versions.items[0]!.id,result.revision_id);
  const detail=await client.skillRevision(session.id,result.catalog_id,result.revision_id);assert.equal(detail.manifest_text,input.manifest_text);assert.equal(detail.provenance.operation_id,'prepare-original');
  final.finish();await final.prepare(session.id,{kind:'enable',catalog_id:result.catalog_id,revision_id:result.revision_id,manifest_text:''});assert.equal(final.phase,'pending');await final.decide('cancel');assert.equal(final.result?.state,'rejected');assert.equal((await client.skillCatalog(session.id,result.catalog_id)).state,'disabled');
  final.finish();await final.prepare(session.id,{kind:'enable',catalog_id:result.catalog_id,revision_id:result.revision_id,manifest_text:''});await final.decide('confirm');assert.equal((await client.skillCatalog(session.id,result.catalog_id)).selected_revision,result.revision_id);
  final.finish();await final.prepare(session.id,{kind:'disable',catalog_id:result.catalog_id,revision_id:'',manifest_text:''});await final.decide('confirm');final.finish();await final.prepare(session.id,{kind:'disable',catalog_id:result.catalog_id,revision_id:'',manifest_text:''});assert.equal(final.result?.review.artifact_sha256,'');await final.decide('confirm');assert.equal(final.result?.state,'no_op');
  assert.equal((await client.snapshot(session.id)).runs.length,0);assert.ok(paths.every(p=>!p.includes('/messages')&&!p.includes('/status')));
 }finally{await stop();await rm(dir,{recursive:true,force:true})}
});
