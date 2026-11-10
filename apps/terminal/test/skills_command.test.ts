import {test} from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';

// Hold the real bounded file reader at its FileHandle.read boundary. Controllers
// and command adapter are production objects; transports are memory-only traps.
for(const competing of ['knowledge','message','navigation'] as const){
 test(`Ink delayed import rechecks ${competing} ownership before Skill prepare`,()=>{
  const source=String.raw`
   import assert from 'node:assert/strict';
   import {mock} from 'node:test';
   import {mkdtemp,writeFile,open,rm} from 'node:fs/promises';
   import {tmpdir} from 'node:os';
   import {join} from 'node:path';
   import {SkillCommands} from './apps/terminal/src/skills.tsx';
   import {SkillOperations,KnowledgeOperations,SessionController} from './packages/client/src/index.ts';
   function deferred(){let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
   const dir=await mkdtemp(join(tmpdir(),'skill-command-'));const path=join(dir,'literal manifest.yaml');
   const entered=deferred(),release=deferred(),other=deferred();let posts=0,persisted=0;
   const client={prepareSkill:async()=>{posts++;throw Error('Skill transport must not be invoked')},prepareKnowledge:()=>other.promise,submitMessage:()=>other.promise};
   const actions=new SkillOperations(client,{save:()=>persisted++}),knowledge=new KnowledgeOperations(client);
   const controller=new SessionController(client,'source',undefined,undefined,undefined,undefined,knowledge,undefined,actions);
   const commands=new SkillCommands(client,actions);let competingResult;
   try{
    await writeFile(path,'id: owned.fixture\n');const probe=await open(path);const prototype=Object.getPrototypeOf(probe);await probe.close();
    const read=prototype.read;
    const held=mock.method(prototype,'read',async function(...args){entered.resolve();await release.promise;return read.apply(this,args)});
    assert.equal(controller.skillBlocked,false);
    const pending=commands.command('source','/skills import-file '+path,()=>controller.skillBlocked).catch(e=>String(e));
    await entered.promise;assert.equal(actions.phase,'idle');assert.equal(persisted,0);
    if(COMPETING==='knowledge')competingResult=knowledge.prepare('source',{kind:'ingest',document_id:'',source_kind:'text',source:'fixture',content:'fixture'}).catch(e=>e);
    else if(COMPETING==='message'){controller.setDraft('ordinary message');competingResult=controller.submit().catch(e=>e)}
    else actions.beginNavigation();
    if(COMPETING!=='navigation')assert.equal(controller.skillBlocked,true);
    release.resolve();const result=await pending;held.mock.restore();
    assert.equal(posts,0,'Skill prepare escaped shared quarantine after local file I/O');
    assert.equal(actions.phase,'idle');assert.equal(actions.recoveryHint,'');assert.equal(persisted,0);
    assert.match(result,COMPETING==='navigation'?/Stale file read/:/Another operation is unresolved/);
   }finally{release.resolve();other.reject(Error('owned transport stopped'));if(competingResult)await competingResult;else await other.promise.catch(()=>{});mock.restoreAll();await rm(dir,{recursive:true,force:true})}
  `.replaceAll('COMPETING',JSON.stringify(competing));
  const result=spawnSync('node',['--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','--input-type=module','-e',source],{encoding:'utf8',timeout:15000});
  assert.equal(result.status,0,result.stderr||result.stdout);
 });
}
