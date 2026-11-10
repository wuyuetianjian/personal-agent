import {APIError} from './errors.ts';
import type {AgentClient} from './index.ts';
import type {OperatorKind,OperatorOperation} from '@personal-agent/protocol';
import {operatorRequestID,parseOperatorOperation} from '@personal-agent/protocol';
import {operatorDisplayText} from './operator.ts';
type Client=Pick<AgentClient,'prepareOperator'|'decideOperator'|'operatorOperation'>;
type Reference={session_id:string;request_id:string;operation_id:string};
interface Options{id?:()=>string;save?:(value:string|null)=>void;restore?:string|null}
// Exact input stays in memory. Persistence contains recovery IDs only, never
// names, target text, token or authority. Unknown transport never retries a POST.
export class OperatorOperations{
 phase:'idle'|'sending'|'pending'|'unknown'|'resolved'='idle';result:OperatorOperation|null=null;
 persistenceWarning='';
 private intent:{kind:OperatorKind;target:string;value:string}|null=null;private decisionIntent:'confirm'|'cancel'|null=null;
 private reference:Reference|null=null;private listeners=new Set<()=>void>();private reading=false;private generation=0;
 private client:Client;private options:Options;
 constructor(client:Client,options:Options={}){this.client=client;this.options=options;if(options.restore){const v=JSON.parse(options.restore);if(!v||Object.keys(v).sort().join(',')!=='operation_id,request_id,session_id'||!operatorRequestID(v.request_id)||(v.operation_id!==''&&!operatorRequestID(v.operation_id))||typeof v.session_id!=='string'||!v.session_id||v.session_id.length>128)throw Error('invalid operator recovery');this.reference={session_id:v.session_id,request_id:v.request_id,operation_id:v.operation_id};this.phase='unknown'}}
 subscribe(fn:()=>void){this.listeners.add(fn);return()=>{this.listeners.delete(fn)}}private emit(){for(const f of this.listeners)f()}
 private persist(){try{this.options.save?.(this.locked&&this.reference?JSON.stringify(this.reference):null);this.persistenceWarning=''}catch{this.persistenceWarning='Cannot persist recovery IDs. Keep these IDs before closing.'}}
 get locked(){return !['idle','resolved'].includes(this.phase)}get sourceSessionID(){return this.reference?.session_id??null}
 get recoveryHint(){return this.reference?`${this.phase}: ${JSON.stringify(this.reference)}. /op recover ${this.reference.session_id} ${this.reference.request_id} (read-only). Do not resend with a new ID.`:''}
 beginNavigation(){return this.locked?null:++this.generation}isNavigationCurrent(g:number){return !this.locked&&this.generation===g}
 private accept(value:OperatorOperation){const ref=this.reference!;const p=parseOperatorOperation(value,ref.session_id,ref.request_id);if(this.intent&&(p.kind!==this.intent.kind||(this.intent.kind==='project.create'?this.intent.target!=='':p.target_id!==this.intent.target)||p.new_value!==this.intent.value))throw Error('operator recovered intent mismatch');if(this.decisionIntent&&p.decision!==this.decisionIntent)throw Error('operator recovered decision mismatch');if(ref.operation_id&&p.operation_id!==ref.operation_id)throw Error('operator reference mismatch');if(this.result){for(const k of ['operation_id','session_id','scope_id','policy_digest','execution_class','kind','target_id','old_value','new_value','revision','expires_at_ms','digest'] as const)if(this.result[k]!==p[k])throw Error('operator preview changed')}this.reference={...ref,operation_id:p.operation_id};this.result=Object.freeze({...p});this.phase=p.state==='pending'?'pending':'resolved';this.persist();this.emit();return this.result}
 async prepare(session:string,kind:OperatorKind,target:string,value:string){if(this.locked)return null;if(kind==='project.create'&&target!=='')throw Error('create target must be empty');const id=(this.options.id??(()=>crypto.randomUUID()))();this.reference={session_id:session,request_id:id,operation_id:id};this.result=null;this.intent=Object.freeze({kind,target,value});this.decisionIntent=null;this.phase='sending';this.generation++;this.emit();try{this.persist();return this.accept(await this.client.prepareOperator(session,kind,target,value,id))}catch(e){this.phase=definite(e)?'idle':'unknown';if(this.phase==='idle')this.reference=null;this.persist();this.emit();throw e}}
 async decide(decision:'confirm'|'cancel'){if(this.phase!=='pending'||!this.result||this.reading)return null;const p=this.result;this.decisionIntent=decision;const id=(this.options.id??(()=>crypto.randomUUID()))();this.reference={session_id:p.session_id,operation_id:p.operation_id,request_id:id};this.phase='sending';this.emit();try{this.persist();return this.accept(await this.client.decideOperator(p.session_id,p.operation_id,p.digest,decision,id))}catch(e){if(definite(e)){this.reference.request_id=p.operation_id;this.decisionIntent=null;}this.phase='unknown';this.persist();this.emit();throw e}}
 async recover(session?:string,request?:string){if(this.phase==='sending'||this.reading)return null;if(this.phase==='resolved'){if((!session&&!request)||(session===this.reference?.session_id&&request===this.reference?.request_id))return this.result;throw Error('dismiss known result before another lookup')}if(session&&request){if(this.reference&&(this.reference.session_id!==session||this.reference.request_id!==request))throw Error('another operator action unresolved');if(!this.reference){if(!operatorRequestID(request))throw Error('invalid recovery ID');this.reference={session_id:session,request_id:request,operation_id:''};this.generation++;this.phase='unknown';this.persist();this.emit()}}if(!this.reference)return null;this.reading=true;try{return this.accept(await this.client.operatorOperation(this.reference.session_id,this.reference.request_id))}catch(e){this.phase='unknown';this.emit();throw e}finally{this.reading=false}}
 finish(){if(this.phase!=='resolved')return;this.reference=null;this.result=null;this.intent=null;this.decisionIntent=null;this.phase='idle';this.persist();this.emit()}
}
function definite(e:unknown){return e instanceof APIError&&['operator_invalid_request','invalid_request','operator_request_conflict','operator_not_found','session_not_found','unauthorized','forbidden_origin','incompatible_protocol','json_required'].includes(e.code)}
export const operatorExactText=(v:string)=>JSON.stringify(v).replace(/[\x7f-\x9f\p{Cf}]/gu,c=>'\\u{'+c.codePointAt(0)!.toString(16)+'}');
export function formatOperatorOperation(p:OperatorOperation){return operatorDisplayText(`Metadata ${p.state}: ${p.kind}\nSession: ${p.session_id}\nScope: ${p.scope_id||'none (chat-only)'}\nTarget${p.kind==='project.create'?' (server project ID)':''}: ${operatorExactText(p.target_id)}${p.execution_class?'\nExecution class: '+p.execution_class:''}\nOld: ${operatorExactText(p.old_value)}\nNew: ${operatorExactText(p.new_value)}\nPolicy: ${p.policy_digest}\nRevision: ${p.revision}\nDigest: ${p.digest}\nExpires: ${new Date(p.expires_at_ms).toISOString()}\nOperation: ${p.operation_id}\n${p.reason}\nCatalog only; not permission revocation. Archive/restore does not stop jobs/runs, revoke tools or remove knowledge. Metadata only; no execution or permission change.`)}
export async function operatorCommand(op:OperatorOperations,session:string,line:string,blocked=false):Promise<string>{
 const args=line.trim().split(/\s+/);const action=args[1];
 if(action==='recover'){await op.recover(args[2],args[3]);return op.recoveryHint}
 if(action==='dismiss'){op.finish();return ''}
 if(blocked)return 'Another mutation is unresolved; no metadata action sent.';
 if(action==='confirm'||action==='cancel'){await op.decide(action);return op.recoveryHint}
 const create=/^\/project create\s([\s\S]+)$/.exec(line);
 if(create){await op.prepare(session,'project.create','',create[1]!);return op.recoveryHint}
 const rename=/^\/(?:op|project) rename\s+(\S+)\s([\s\S]+)$/.exec(line);
 if(rename){await op.prepare(session,'project.rename',rename[1]!,rename[2]!);return op.recoveryHint}
 if((action==='archive'||action==='restore')&&args.length===3){await op.prepare(session,action==='archive'?'project.archive':'project.restore',args[2]!,action==='archive'?'archived':'active');return op.recoveryHint}
 if(action==='read'&&args.length===3){await op.prepare(session,'notification.mark-read',args[2]!,'read');return op.recoveryHint}
 return '/project create NAME; /project archive ID; /project restore ID; /op rename ID NEW NAME; /op read ID (preview only); /op confirm; /op cancel; /op recover [SESSION REQUEST] (read-only); /op dismiss';
}
