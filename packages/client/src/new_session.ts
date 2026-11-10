import {parseProjectLabel} from '@personal-agent/protocol';
import type {ProjectLabel,SessionAdmissionView,SessionView} from '@personal-agent/protocol';
import type {AgentClient,SessionMode} from './index.ts';
import {APIError,SessionAdmissionNotSentError} from './errors.ts';

export interface ActiveProject extends ProjectLabel {execution_class:'metadata_only'|'legacy_policy'}
export interface ActiveProjects {schema_version:1;items:ActiveProject[];next_cursor:string}
export function parseActiveProjects(value:unknown):ActiveProjects {
 if(!value||typeof value!=='object'||Array.isArray(value))throw Error('invalid active catalog');
 const v=value as Record<string,unknown>;
 if(Object.keys(v).sort().join(',')!=='items,next_cursor,schema_version'||v['schema_version']!==1||!Array.isArray(v['items'])||v['items'].length>50||typeof v['next_cursor']!=='string'||v['next_cursor'].length>128||!/^[A-Za-z0-9_-]*$/.test(v['next_cursor']))throw Error('invalid active catalog');
 const ids=new Set<string>();for(const raw of v['items']) {if(!raw||typeof raw!=='object')throw Error('invalid catalog entry');const {execution_class,...label}=raw;parseProjectLabel(label);if(!['metadata_only','legacy_policy'].includes(execution_class)||ids.has(label.project_id))throw Error('invalid catalog entry');ids.add(label.project_id)}
 return v as unknown as ActiveProjects;
}
// Escape at the presentation boundary, rather than deleting bytes and aliasing IDs.
export function labelText(text:string):string {return text.replaceAll('\\','\\\\').replace(/[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/gu,c=>[...c].map(x=>'\\u'+x.codePointAt(0)!.toString(16).padStart(4,'0')).join(''))}
// JSON already quotes backslashes and C0 controls. Escape remaining display
// controls as UTF-16 JSON escapes, so copy/paste still decodes to the exact ID.
export function displayJSON(value:unknown,space?:number):string {const json=JSON.stringify(value,null,space);return json===undefined?'undefined':json.replace(/[\p{Cf}\p{Zl}\p{Zp}\u007f-\u009f]/gu,c=>c.split('').map(x=>'\\u'+x.charCodeAt(0).toString(16).padStart(4,'0')).join(''))}
export function projectLabelText(label?:ProjectLabel):string {return label?`Label ${labelText(label.project_id)} · ${labelText(label.name)} · generation ${label.generation} / revision ${label.revision}`:'No project label / 无项目标签'}
export interface NewSessionInput {mode:SessionMode;model_id?:string;mcp:boolean;catalog_project_id:string;content:string}
export interface NewSessionIntent extends NewSessionInput {request_id:string}
interface Reference {request_id:string;session_id?:string}
interface Options {id?:()=>string;save?:(serialized:string|null)=>void;restore?:string|null}
function id(value:unknown,max=128):value is string {return typeof value==='string'&&value.length>0&&new TextEncoder().encode(value).length<=max&&!/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value)}
function equalLabel(a?:ProjectLabel,b?:ProjectLabel):boolean {return a===undefined&&b===undefined||!!a&&!!b&&a.project_id===b.project_id&&a.name===b.name&&a.generation===b.generation&&a.revision===b.revision}
export function isPreclaimRefusal(error:unknown):boolean {return error instanceof APIError&&((error.status===409&&error.code==='project_label_unavailable')||(error.status===400&&['invalid_request','explicit_mode_required','invalid_query'].includes(error.code))||(error.status===503&&error.code==='service_capacity')||([401,403,415,426].includes(error.status)&&['unauthorized','forbidden_origin','json_required','incompatible_protocol'].includes(error.code)))}
// Single-use admission coordinator. Never POSTs from restore, recovery, cancel or
// navigation. Initial content belongs to a separate message intent, not admission;
// recovery can inspect it but must never submit/replay it.
export class NewSessionOperations {
 private stateRequestID='';private reference:Reference|null=null;private intent:Readonly<NewSessionIntent>|null=null;private listeners=new Set<()=>void>();private generation=0;private reading=false;
 private readonly client:AgentClient;private readonly options:Options;
 phase:'idle'|'sending'|'unknown'|'not_found'|'starting'|'unresolved'|'revoked'|'confirmed'='idle';
 result:SessionView|null=null;admission:SessionAdmissionView|null=null;storageWarning='';cancelRequested=false;
 constructor(client:AgentClient,options:Options={}){this.client=client;this.options=options;if(options.restore){const v=JSON.parse(options.restore) as Reference;if(!v||Object.keys(v).some(k=>!['request_id','session_id'].includes(k))||!id(v.request_id)||v.session_id!==undefined&&!id(v.session_id))throw Error('invalid admission recovery identity');this.reference={...v};this.stateRequestID=v.request_id;this.phase='unknown'}}
 get pending(){return this.intent}
 get requestID(){return this.reference?.request_id??''}
 get sessionID(){return this.result?.id??this.reference?.session_id??''}
 get locked(){return this.phase!=='idle'}
 get recoveryHint(){return this.reference?`${this.phase}: admission ${labelText(this.requestID)}${this.sessionID?' / session '+labelText(this.sessionID):''}. /new recover ${labelText(this.requestID)} (GET only). No automatic retry or initial-content replay. ${this.cancelRequested?'Cancel requested, NOT proof setup/process never started. ':''}${this.storageWarning}`:''}
 subscribe(fn:()=>void){this.listeners.add(fn);return ()=>{this.listeners.delete(fn)}}
 private emit(){for(const fn of this.listeners)fn()}
 private persist(){try{this.options.save?.(this.reference?JSON.stringify(this.reference):null)}catch{this.storageWarning='Recovery storage unavailable; retain the displayed request/session IDs before leaving.'}}
 beginNavigation():number|null {return this.locked?null:++this.generation}
 isNavigationCurrent(version:number){return !this.locked&&version===this.generation}
 async create(input:NewSessionInput):Promise<SessionView|null>{
  if(this.locked){const i=this.intent;if(i&&(i.mode!==input.mode||i.model_id!==input.model_id||i.mcp!==input.mcp||i.catalog_project_id!==input.catalog_project_id||i.content!==input.content))throw Error('different admission intent unresolved');return null}
  const request=(this.options.id??(()=>crypto.randomUUID()))();if(!id(request)||!['chat_only','default','plan','acceptEdits'].includes(input.mode)||typeof input.mcp!=='boolean'||input.mcp&&input.mode==='chat_only'||typeof input.content!=='string'||typeof input.catalog_project_id!=='string'||input.catalog_project_id!==''&&(!id(input.catalog_project_id,512)||input.catalog_project_id.includes('\0')))throw Error('invalid new-session intent');
  this.intent=Object.freeze({...input,request_id:request});this.reference={request_id:request};this.phase='sending';this.stateRequestID=request;this.result=null;this.admission=null;this.cancelRequested=false;this.generation++;this.persist();this.emit();
  try{const i=this.intent;const s=await this.client.createSession(i.mode,i.request_id,i.model_id,i.mcp,i.catalog_project_id);this.checkSession(s);this.result=s;this.reference={request_id:request,session_id:s.id};this.phase='confirmed';this.persist();this.emit();return s}
  catch(e){if(e instanceof SessionAdmissionNotSentError||isPreclaimRefusal(e)){this.reference=null;this.intent=null;this.phase='idle';this.persist()}else this.phase='unknown';this.emit();throw e}
 }
 private checkSession(s:SessionView){if(!id(s.id))throw Error('invalid admission session ID');const i=this.intent;if(i&&(s.mode!==i.mode||i.model_id!==undefined&&s.model_id!==i.model_id||(s.project_label?.project_id??'')!==i.catalog_project_id))throw Error('admission intent mismatch');if(this.reference?.session_id&&this.reference.session_id!==s.id)throw Error('admission session mismatch');if(this.admission&&!equalLabel(this.admission.project_label,s.project_label))throw Error('admission snapshot mismatch')}
 async recover(requestID?:string):Promise<SessionView|null>{
  if(requestID&&this.reference&&this.requestID!==requestID)throw Error('different admission unresolved');if(this.phase==='sending'||this.reading)return null;if(this.phase==='confirmed')return this.result;
  if(requestID){
   if(!id(requestID))throw Error('invalid admission request ID');
   if(this.reference&&this.requestID!==requestID)throw Error('different admission unresolved');
   if(this.phase==='idle'){
    if(this.stateRequestID!==requestID){this.result=null;this.admission=null;this.intent=null;this.cancelRequested=false}
    else if(this.result){this.reference={request_id:requestID,session_id:this.result.id};this.phase='confirmed';this.generation++;this.persist();this.emit();return this.result}
    else if(this.admission)this.reference={request_id:requestID,session_id:this.admission.session_id};
   }
   this.stateRequestID=requestID;this.reference={...this.reference,request_id:requestID};this.phase='unknown';this.generation++;this.persist();this.emit();
  }
  if(!this.reference)return null;this.reading=true;
  try{const a=await this.client.admission(this.reference.request_id);if(this.reference.session_id&&this.reference.session_id!==a.session_id)throw Error('admission identity mismatch');if(this.intent&&a.state==='complete'&&(a.project_label?.project_id??'')!==this.intent.catalog_project_id)throw Error('admission label mismatch');if(this.intent&&a.mcp_opt_in!==this.intent.mcp)throw Error('admission MCP intent mismatch');this.admission=a;this.reference={request_id:a.request_id,session_id:a.session_id};this.phase=a.state==='complete'?'unknown':a.state as 'starting'|'unresolved'|'revoked';this.persist();this.emit();if(a.state!=='complete')return null;
   const snapshot=await this.client.snapshot(a.session_id);this.checkSession(snapshot.session);this.result=snapshot.session;this.phase='confirmed';this.emit();return this.result;
  }catch(e){if(e instanceof APIError&&e.code==='admission_not_found'&&!this.admission)this.phase='not_found';this.emit();throw e}finally{this.reading=false}
 }
 cancel(){if(!this.locked)return;this.cancelRequested=true;this.emit()}
 // Explicit acknowledgement is only safe for a server-settled failed/revoked
 // claim. Not-found is not proof that a request cannot still arrive/commit.
 abandon(){if(this.reading||!['unresolved','revoked'].includes(this.phase))throw Error('admission unresolved: inspect original request first');this.reference=null;this.intent=null;this.phase='idle';this.generation++;this.persist();this.emit()}
 applied(sessionID:string){if(this.phase!=='confirmed'||this.result?.id!==sessionID)return;this.reference=null;this.intent=null;this.phase='idle';this.persist();this.emit()}
}
