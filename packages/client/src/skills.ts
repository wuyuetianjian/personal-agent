import {HookOperations} from './hooks.ts';
import {SkillRunOperations} from './skill_runs.ts';
import {APIError,SkillNotSentError} from './errors.ts';
import type {AgentClient} from './index.ts';
import type {SkillInput,SkillOperation,SkillReview} from '@personal-agent/protocol';
import {operatorRequestID,knowledgeOwner,parseSkillOperation,parseSkillReview,validateSkillInput,skillSame} from '@personal-agent/protocol';
type Client=Pick<AgentClient,'prepareSkill'|'decideSkill'|'skillOperation'>;
type Reference={session_id:string;request_id:string;operation_id:string};
interface Options{hookRestore?:string|null;saveHook?:(value:string|null)=>void;id?:()=>string;save?:(value:string|null)=>void;restore?:string|null;runRestore?:string|null;saveRun?:(value:string|null)=>void}
export async function skillSHA256(text:string){return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',new TextEncoder().encode(text))),x=>x.toString(16).padStart(2,'0')).join('')}
// Go encoding/json preserves struct declaration order and escapes HTML and
// paragraph separators. Rebuild this exact order, never trust wire key order.
export function skillReviewJSON(r:SkillReview){
 const m=r.manifest;
 const value={parser_version:r.parser_version,review_version:r.review_version,artifact_sha256:r.artifact_sha256,content_sha256:r.content_sha256,content_bytes:r.content_bytes,canonical_manifest:r.canonical_manifest,manifest:{id:m.id,version:m.version,name:m.name,description:m.description,status:m.status,inputs:m.inputs===null?null:Object.fromEntries(Object.keys(m.inputs).sort().map(k=>[k,{type:m.inputs![k]!.type}])),requires:{capabilities:m.requires.capabilities},permissions:{max_level:m.permissions.max_level},privacy:{max_external_trust:m.privacy.max_external_trust},budget:{max_iterations:m.budget.max_iterations,max_tool_calls:m.budget.max_tool_calls},verification:{min_coverage:m.verification.min_coverage,allow_conflicts:m.verification.allow_conflicts},workflow:{nodes:m.workflow.nodes?.map(n=>({id:n.id,capability:n.capability,role:n.role,depends_on:n.depends_on}))??null}},targets:r.targets.map(t=>({node_id:t.node_id,kind:t.kind,id:t.id,support:t.support})),evaluation:r.evaluation,execution:r.execution,execution_reason:r.execution_reason,notice:r.notice};
 const encode=(v:unknown)=>JSON.stringify(v).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'));
 // Reuse the validated Go canonical manifest token to preserve -0 and Go
 // floating-point spellings. Parsing/re-stringifying would change its hash.
 return '{'+Object.entries(value).map(([k,v])=>encode(k)+':'+(k==='manifest'&&r.canonical_manifest!==''?r.canonical_manifest:encode(v))).join(',')+'}';
}
export async function verifySkillReview(r:SkillReview,raw?:string){parseSkillReview(r,r.artifact_sha256==='');if(r.artifact_sha256!==''&&await skillSHA256(r.canonical_manifest)!==r.content_sha256)throw Error('skill canonical hash mismatch');if(raw!==undefined&&(await skillSHA256(raw)!==r.artifact_sha256||new TextEncoder().encode(raw).length!==r.content_bytes))throw Error('skill artifact mismatch')}
export async function verifySkillOperation(p:SkillOperation){await verifySkillReview(p.review);if(await skillSHA256(skillReviewJSON(p.review))!==p.review_sha256)throw Error('skill review hash mismatch');if(p.review.artifact_sha256!==''&&'skill-'+await skillSHA256(p.review.manifest.id)!==p.catalog_id)throw Error('skill catalog identity mismatch')}
export async function matchSkillIntent(p:SkillOperation,i:SkillInput){if(p.kind!==i.kind||i.kind!=='import'&&p.catalog_id!==i.catalog_id||i.kind==='enable'&&p.revision_id!==i.revision_id)throw Error('skill intent mismatch');await verifySkillOperation(p);if(i.kind==='import')await verifySkillReview(p.review,i.manifest_text)}
function freeze<T>(value:T):T{if(value&&typeof value==='object'){for(const v of Object.values(value))freeze(v);Object.freeze(value)}return value}
const immutable=['operation_id','session_id','scope_id','policy_digest','kind','catalog_id','revision_id','expected_generation','expected_revision','old_state','old_selected_revision','new_state','new_selected_revision','parser_version','review_version','review','review_sha256','expires_at_ms','digest'] as const;
// The lifecycle never sends after recovery, and never persists manifest text.
// A pending review locks all other mutation surfaces until an explicit decision.
export class SkillOperations {
 readonly runs:SkillRunOperations;readonly hooks:HookOperations;
 phase:'idle'|'sending'|'pending'|'unknown'|'resolved'='idle';result:SkillOperation|null=null;persistenceWarning='';
 private routingControl=false;
 private intent:SkillInput|null=null;private decisionIntent:'confirm'|'cancel'|null=null;private reference:Reference|null=null;
 private listeners=new Set<()=>void>();private reading=false;private generation=0;private used=new Set<string>();private client:Client;private options:Options;
 constructor(client:Client,options:Options={}){this.client=client;this.options=options;this.hooks=new HookOperations(client as AgentClient,{...(options.hookRestore?{restore:options.hookRestore}:{}),...(options.saveHook?{save:options.saveHook}:{})});this.hooks.subscribe(()=>{this.generation++;this.emit()});this.runs=new SkillRunOperations(client as AgentClient,{...(options.runRestore?{restore:options.runRestore}:{}),...(options.saveRun?{save:options.saveRun}:{})});this.runs.subscribe(()=>{this.generation++;this.emit()});if(options.restore){const v=JSON.parse(options.restore);if(!v||Object.keys(v).sort().join(',')!=='operation_id,request_id,session_id'||!operatorRequestID(v.request_id)||(v.operation_id!==''&&!operatorRequestID(v.operation_id))||!knowledgeOwner(v.session_id))throw Error('invalid skill recovery');this.reference={session_id:v.session_id,request_id:v.request_id,operation_id:v.operation_id};this.used.add(v.request_id);if(v.operation_id)this.used.add(v.operation_id);this.phase='unknown'}}
 subscribe(fn:()=>void){this.listeners.add(fn);return()=>{this.listeners.delete(fn)}}private emit(){for(const fn of this.listeners)fn()}
 private persist(){try{this.options.save?.(this.locked&&this.reference?JSON.stringify(this.reference):null);this.persistenceWarning=''}catch{this.persistenceWarning='Cannot persist recovery IDs. Keep these IDs before closing.'}}
 private id(){const id=(this.options.id??(()=>crypto.randomUUID()))();if(!operatorRequestID(id)||this.used.has(id))throw new SkillNotSentError('unique skill request ID required');this.used.add(id);return id}
 get locked(){return this.routingControl||this.hooks.locked||this.runs.locked||this.phase!=='idle'&&this.phase!=='resolved'}get sourceSessionID(){return this.hooks.sourceSessionID??this.runs.sourceSessionID??this.reference?.session_id??null}
 get exactManifestText(){return this.intent?.kind==='import'?this.intent.manifest_text:null}
 get recoveryHint(){if(this.hooks.locked)return this.hooks.recoveryHint;if(this.runs.locked)return this.runs.recoveryHint;return this.reference?`${this.phase}: ${JSON.stringify(this.reference)}. Read the original skill request only; do not resend or use a new ID.`:''}
 beginNavigation(){return this.locked?null:++this.generation}isNavigationCurrent(g:number){return !this.locked&&g===this.generation}
 private async accept(value:SkillOperation){const ref=this.reference!;const p=freeze(parseSkillOperation(structuredClone(value),ref.session_id,ref.request_id));await verifySkillOperation(p);if(this.intent)await matchSkillIntent(p,this.intent);if(this.decisionIntent&&p.decision!==this.decisionIntent)throw Error('skill recovered decision mismatch');if(ref.operation_id&&p.operation_id!==ref.operation_id)throw Error('skill reference mismatch');if(this.result&&immutable.some(k=>!skillSame(this.result![k],p[k])))throw Error('skill preview changed');this.reference={...ref,operation_id:p.operation_id};this.used.add(p.operation_id);if(p.decision_request_id)this.used.add(p.decision_request_id);this.result=freeze(structuredClone(p));this.phase=p.state==='pending'?'pending':'resolved';this.persist();this.emit();return this.result}
 async controlRun(session:string,run:string,action:'cancel'|'resume'){
  if(this.locked)return null;
  if(!knowledgeOwner(session)||!operatorRequestID(run)||!['cancel','resume'].includes(action))throw new SkillNotSentError('invalid session run control');
  // Fence repeated keys/navigation before the first asynchronous kind lookup.
  this.routingControl=true;this.generation++;this.emit();const client=this.client as AgentClient;
  try{
   let skill=false;
   try{await client.skillRun(session,run);skill=true}catch(e){
    if(!(e instanceof APIError&&e.status===404&&e.code==='not_found'))throw e;
    // The canonical Skill read establishes absence only. Preserve model controls
    // only for a run independently present in this exact session snapshot.
    const snapshot=await client.snapshot(session);if(snapshot.session.id!==session||!snapshot.runs.some(r=>r.id===run))throw new SkillNotSentError('run is not in this session');
   }
   if(this.runs.phase==='resolved')this.runs.finish();
   if(skill){this.routingControl=false;return await this.runs.control(session,run,action)}
   return await (action==='cancel'?client.cancelRun(run,crypto.randomUUID()):client.resumeRun(run,crypto.randomUUID()));
  }finally{this.routingControl=false;this.emit()}
 }
 async prepare(session:string,input:SkillInput){if(this.locked||this.reading)return null;validateSkillInput(input);if(!knowledgeOwner(session))throw new SkillNotSentError('invalid skill owner');const id=this.id();this.reference={session_id:session,request_id:id,operation_id:id};this.result=null;this.intent=Object.freeze({...input});this.decisionIntent=null;this.phase='sending';this.generation++;this.persist();this.emit();try{return await this.accept(await this.client.prepareSkill(session,this.intent,id))}catch(e){this.phase=e instanceof SkillNotSentError||isDefiniteSkillRefusal(e)?'idle':'unknown';if(this.phase==='idle'){this.reference=null;this.intent=null}this.persist();this.emit();throw e}}
 async decide(decision:'confirm'|'cancel'){if(this.phase!=='pending'||!this.result||this.reading)return null;if(!['confirm','cancel'].includes(decision))throw new SkillNotSentError('invalid skill decision');const p=this.result;const id=this.id();this.decisionIntent=decision;this.reference={session_id:p.session_id,operation_id:p.operation_id,request_id:id};this.phase='sending';this.persist();this.emit();try{return await this.accept(await this.client.decideSkill(p.session_id,p.operation_id,p.digest,decision,id))}catch(e){if(e instanceof SkillNotSentError||isDefiniteSkillRefusal(e)){this.reference.request_id=p.operation_id;this.decisionIntent=null}this.phase=e instanceof SkillNotSentError?'pending':'unknown';this.persist();this.emit();throw e}}
 async recover(session?:string,request?:string){if(this.hooks.locked){await this.hooks.recover(session,request);return null}if(this.runs.locked){await this.runs.recover(session,request);return null;}if(this.phase==='sending'||this.reading)return null;if(this.phase==='resolved'){if((!session&&!request)||(session===this.reference?.session_id&&request===this.reference?.request_id))return this.result;throw Error('dismiss known skill result before another lookup')}if((session===undefined)!==(request===undefined))throw Error('recovery requires both original IDs');if(session!==undefined&&request!==undefined){if(!knowledgeOwner(session)||!operatorRequestID(request))throw Error('invalid skill recovery IDs');if(this.reference&&(this.reference.session_id!==session||this.reference.request_id!==request))throw Error('another skill action unresolved');if(!this.reference){this.reference={session_id:session,request_id:request,operation_id:''};this.used.add(request);this.intent=null;this.decisionIntent=null;this.result=null;this.generation++;this.phase='unknown';this.persist();this.emit()}}if(!this.reference)return null;this.reading=true;try{return await this.accept(await this.client.skillOperation(this.reference.session_id,this.reference.request_id))}catch(e){this.phase='unknown';this.emit();throw e}finally{this.reading=false}}
 finish(){if(this.phase!=='resolved'||this.reading)return;this.reference=null;this.result=null;this.intent=null;this.decisionIntent=null;this.phase='idle';this.persist();this.emit()}
}
export function isDefiniteSkillRefusal(e:unknown){return e instanceof APIError&&new Map([['skill_invalid_request',400],['skill_request_conflict',409],['skill_not_found',404],['session_not_found',404],['invalid_request',400],['invalid_query',400],['unauthorized',401],['forbidden_origin',403],['incompatible_protocol',426],['json_required',415]]).get(e.code)===e.status}
export const skillExact=(v:unknown)=>JSON.stringify(v,null,2).replace(/[\x7f-\x9f\p{Cf}]/gu,c=>'\\u{'+c.codePointAt(0)!.toString(16)+'}');
export function formatSkillOperation(p:SkillOperation){return `Skill catalog metadata review: ${p.state}\nEnabled only selects an exact revision. Evaluation not_run. Execution unavailable: bridge_not_implemented.\nUntrusted literal evidence:\n${skillExact(p)}`}
