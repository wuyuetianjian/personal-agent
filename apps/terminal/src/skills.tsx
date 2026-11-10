import React from 'react';
import {Box,Text} from 'ink';
import {SkillOperations,formatSkillOperation,skillExact,safeTerminalText} from '@personal-agent/client';
import type {AgentClient} from '@personal-agent/client';
import {readSkillFile} from './skills_cli.ts';
import {mkdirSync,readFileSync,writeFileSync,renameSync,unlinkSync} from 'node:fs';
import {join} from 'node:path';
import {homedir} from 'node:os';
import {createHash} from 'node:crypto';
export function terminalSkills(client:AgentClient){
 const dir=join(process.env['XDG_STATE_HOME']||join(homedir(),'.local','state'),'personal-agent');const path=join(dir,'skill-'+createHash('sha256').update(client.baseURL).digest('hex')+'.json');
 const runPath=join(dir,'skill-run-'+createHash('sha256').update(client.baseURL).digest('hex')+'.json');let runRestore:string|null=null;try{runRestore=readFileSync(runPath,'utf8');if(Buffer.byteLength(runRestore)>1024)throw Error('oversized run recovery')}catch(e){if((e as NodeJS.ErrnoException).code!=='ENOENT')throw e}
 let restore:string|null=null;try{restore=readFileSync(path,'utf8');if(Buffer.byteLength(restore)>1024)throw Error('oversized recovery')}catch(e){if((e as NodeJS.ErrnoException).code!=='ENOENT')throw e}
 return new SkillOperations(client,{restore,runRestore,saveRun:value=>{mkdirSync(dir,{recursive:true,mode:0o700});if(value===null){try{unlinkSync(runPath)}catch(e){if((e as NodeJS.ErrnoException).code!=='ENOENT')throw e}}else{writeFileSync(runPath+'.tmp',value,{mode:0o600});renameSync(runPath+'.tmp',runPath)}},save:value=>{mkdirSync(dir,{recursive:true,mode:0o700});if(value===null){try{unlinkSync(path)}catch(e){if((e as NodeJS.ErrnoException).code!=='ENOENT')throw e}}else{writeFileSync(path+'.tmp',value,{mode:0o600});renameSync(path+'.tmp',path)}}});
}
export const skillHelp='/skills [next] · /skills show CATALOG [REVISION] · /skills versions CATALOG [next] · /skills validate-file PATH · /skills import-file PATH (entire literal remainder, no shell quotes) · /skills enable CATALOG REVISION · /skills disable CATALOG · /skills recover [SESSION REQUEST] · /skills confirm|cancel|dismiss. Metadata only; no execution / 仅目录元数据。';
export class SkillCommands{
 private cursor='';private versionCursor='';private versionCatalog='';private generation=0;
 constructor(private client:AgentClient,private actions:SkillOperations){}
 async command(session:string,value:string,isBlocked:()=>boolean):Promise<string>{
  const match=/^\/skills(?: ([\s\S]*))?$/.exec(value);if(!match)return skillHelp;const raw=match[1]??'',args=raw.trim().split(/\s+/),action=args[0]??'';
  if(action==='help')return skillHelp;
  if(action==='dismiss'&&args.length===1){this.actions.finish();return 'Skill result dismissed.'}
  if(action==='recover'&&(args.length===1||args.length===3)){await this.actions.recover(args[1],args[2]);return this.actions.recoveryHint}
  if(['confirm','cancel'].includes(action)&&args.length===1){await this.actions.decide(action as 'confirm'|'cancel');return this.actions.recoveryHint}
  if(this.actions.locked||isBlocked())return this.actions.recoveryHint||'Another operation is unresolved.';
  const g=++this.generation,nav=this.actions.beginNavigation();if(nav===null)return this.actions.recoveryHint;
  const current=()=>g===this.generation&&this.actions.isNavigationCurrent(nav);
  if(action==='validate-file'||action==='import-file'){
   const prefix=action+' ';if(!raw.startsWith(prefix)||!raw.slice(prefix.length))return skillHelp;
   const text=await readSkillFile(raw.slice(prefix.length));
   if(!current())return 'Stale file read discarded.';
   // File acquisition does not reserve a mutation or create recovery IDs.
   // Another controller may have acquired quarantine while the read awaited.
   // Recheck its current state with no await between authorization and prepare.
   if(this.actions.locked||isBlocked())return this.actions.recoveryHint||'Another operation is unresolved.';
   if(action==='import-file'){
    await this.actions.prepare(session,{kind:'import',catalog_id:'',revision_id:'',manifest_text:text});
    return this.actions.recoveryHint;
   }
   const review=await this.client.validateSkill(session,text);return current()?skillExact(review):'Stale validation discarded.';
  }
  if(action==='enable'&&args.length===3||action==='disable'&&args.length===2){await this.actions.prepare(session,{kind:action,catalog_id:args[1]!,revision_id:args[2]??'',manifest_text:''});return this.actions.recoveryHint}
  if(action===''||action==='next'&&args.length===1){if(action==='next'&&!this.cursor)return 'No next skill page.';const page=await this.client.listSkills(session,action==='next'?this.cursor:'');if(!current())return 'Stale list discarded.';this.cursor=page.next_cursor;return skillExact(page)}
  if(action==='versions'&&(args.length===2||args.length===3&&args[2]==='next')){if(args[2]&&(this.versionCatalog!==args[1]||!this.versionCursor))return 'Read first version page for this exact catalog.';const page=await this.client.skillVersions(session,args[1]!,args[2]?this.versionCursor:'');if(!current())return 'Stale versions discarded.';this.versionCatalog=args[1]!;this.versionCursor=page.next_cursor;return skillExact(page)}
  if(action==='show'&&(args.length===2||args.length===3)){const detail=args[2]?await this.client.skillRevision(session,args[1]!,args[2]):await this.client.skillCatalog(session,args[1]!);return current()?skillExact(detail):'Stale detail discarded.'}
  return skillHelp;
 }
}
export function SkillView({actions}:{actions:SkillOperations}){if(actions.phase==='idle')return null;return <Box flexDirection="column" borderStyle="round"><Text>Skill metadata / 技能目录 · {actions.phase}</Text>{actions.result&&<Text>{safeTerminalText(formatSkillOperation(actions.result))}</Text>}{actions.exactManifestText!==null&&<Text>{skillExact(actions.exactManifestText)}</Text>}<Text>{safeTerminalText(actions.recoveryHint)}</Text><Text>{actions.persistenceWarning}</Text><Text>{actions.phase==='pending'?'y confirm exact metadata / n cancel':actions.phase==='unknown'?'r read-only recovery':actions.phase==='resolved'?'Enter / Esc dismiss skill result (never approves tool)':'Sending; no retry'}</Text></Box>}
