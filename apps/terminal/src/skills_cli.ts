import {open} from 'node:fs/promises';
import {constants} from 'node:fs';
import {APIError,SkillNotSentError,isDefiniteSkillRefusal,skillExact,formatSkillOperation} from '@personal-agent/client';
import type {AgentClient} from '@personal-agent/client';
import {knowledgeOwner,operatorRequestID,validateSkillTarget} from '@personal-agent/protocol';
// O_NONBLOCK avoids waiting on FIFOs; O_NOFOLLOW refuses symbolic links.
export async function readSkillFile(path:string,max=65536):Promise<string>{
 const file=await open(path,constants.O_RDONLY|constants.O_NOFOLLOW|constants.O_NONBLOCK);
 try{const stat=await file.stat();if(!stat.isFile()||stat.size>max)throw Error('bounded regular UTF-8 file required');const bytes=Buffer.alloc(max+1);let size=0;while(size<bytes.length){const r=await file.read(bytes,size,bytes.length-size,null);if(!r.bytesRead)break;size+=r.bytesRead}if(size>max)throw Error('file exceeds '+max+' bytes');return new TextDecoder('utf-8',{fatal:true,ignoreBOM:true}).decode(bytes.subarray(0,size))}finally{await file.close()}
}
export interface SkillPrintOptions {skill:string;session:string;request:string;operation:string;digest:string;format:string;catalog:string;revision:string;manifestFile:string;cursor:string;skillFields:string[]}
export function validateSkillPrint(o:SkillPrintOptions){
 const allowed:Record<string,string[]>={list:['cursor'],versions:['catalog','cursor'],show:['catalog','revision'],validate:['manifestFile'],import:['manifestFile','request'],enable:['catalog','revision','request'],disable:['catalog','request'],confirm:['operation','digest','request'],cancel:['operation','digest','request'],lookup:['request']};
 if(!knowledgeOwner(o.session)||!['text','json'].includes(o.format)||!Object.hasOwn(allowed,o.skill))throw Error('skill requires existing session, action and text/json format');
 if(o.skillFields.some(f=>!allowed[o.skill]!.includes(f)))throw Error('unused or incompatible skill flags');
 if(['versions','show','enable','disable'].includes(o.skill))validateSkillTarget(o.catalog,o.revision||undefined);
 if(o.skill==='enable'&&!o.revision)throw Error('exact revision required');
 if(['validate','import'].includes(o.skill)&&!o.manifestFile)throw Error('explicit manifest file required');
 if(['import','enable','disable','confirm','cancel','lookup'].includes(o.skill)&&!operatorRequestID(o.request))throw Error('stable request ID required');
 if(['confirm','cancel'].includes(o.skill)&&(!operatorRequestID(o.operation)||o.operation===o.request||!/^[a-f0-9]{64}$/.test(o.digest)))throw Error('exact operation, digest and distinct request required');
}
export async function skillPrint(c:AgentClient,o:SkillPrintOptions,write:(s:string)=>void){
 validateSkillPrint(o);let sent=false;
 try{
  const text=['validate','import'].includes(o.skill)?await readSkillFile(o.manifestFile):'';
  sent=true;let result;
  if(o.skill==='list')result=await c.listSkills(o.session,o.cursor);
  else if(o.skill==='versions')result=await c.skillVersions(o.session,o.catalog,o.cursor);
  else if(o.skill==='show')result=o.revision?await c.skillRevision(o.session,o.catalog,o.revision):await c.skillCatalog(o.session,o.catalog);
  else if(o.skill==='validate')result=await c.validateSkill(o.session,text);
  else if(o.skill==='lookup')result=await c.skillOperation(o.session,o.request);
  else if(o.skill==='confirm'||o.skill==='cancel')result=await c.decideSkill(o.session,o.operation,o.digest,o.skill,o.request);
  else result=await c.prepareSkill(o.session,{kind:o.skill,manifest_text:text,catalog_id:o.catalog,revision_id:o.revision},o.request);
  write((o.format==='json'?JSON.stringify(result):'operation_id' in result?formatSkillOperation(result):skillExact(result))+'\n');
  return 'state' in result&&['rejected','expired'].includes(result.state)?1:0;
 }catch(e){
  const read=['list','versions','show','validate','lookup'].includes(o.skill),decision=['confirm','cancel'].includes(o.skill);
  const local=!sent||e instanceof SkillNotSentError,definite=local||isDefiniteSkillRefusal(e);
  const recovery=local?'':decision?(definite?o.operation:o.request):o.skill==='lookup'?o.request:read||definite?'':o.request;
  const args=recovery?['--url',c.baseURL,'--skill','lookup','--session',o.session,'--request-id',recovery,'--format',o.format]:[];
  const result={state:local||(!read&&!decision&&definite)?'refused':read||definite?'unresolved':'unknown',code:local?'skill_not_sent':definite&&e instanceof APIError?e.code:'skill_outcome_unconfirmed',session_id:o.session,request_id:o.request,operation_id:recovery?(o.operation||o.request):'',recovery_request_id:recovery,recovery_args:args,reason:local?'No request sent. Choose a bounded regular UTF-8 manifest file and valid fields.':'Response not confirmed. Only read the original request; never automatically retry POST.'};
  write((o.format==='json'?JSON.stringify(result):skillExact(result))+'\n');return result.state==='refused'?1:2;
 }
}
