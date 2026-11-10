import type {AgentClient} from './index.ts';
import type {JobOperationRecord} from '@personal-agent/protocol';
import {APIError} from './errors.ts';
type Kind='create'|'stop'|'decision';
type Reference={session_id:string;request_id:string;kind:Kind|'lookup';job_id:string};
type Intent=Reference&{kind:Kind;content:string;effects:boolean;origin_run:string;approval:string;digest:string;decision:'approved'|'denied'};
interface Options{id?:()=>string;save?:(value:string|null)=>void;restore?:string|null}
// Metadata only survives reload. Exact mutation payload is frozen in memory.
export class JobOperations{
 private generation=0;private reference:Reference|null=null;private intent:Intent|null=null;private reading=false;private listeners=new Set<()=>void>();
 phase:'idle'|'sending'|'unknown'|'confirmed'='idle';result:JobOperationRecord|null=null;
 private client:AgentClient;private options:Options;
 constructor(client:AgentClient,options:Options={}){this.client=client;this.options=options;if(options.restore){const v=JSON.parse(options.restore) as Reference;if(!v||typeof v.session_id!=='string'||!v.session_id||typeof v.request_id!=='string'||!v.request_id||typeof v.job_id!=='string'||!['create','stop','decision','lookup'].includes(v.kind))throw new Error('invalid job recovery');this.reference={session_id:v.session_id,request_id:v.request_id,job_id:v.job_id,kind:v.kind};this.phase='unknown'}}
 subscribe(fn:()=>void){this.listeners.add(fn);return()=>{this.listeners.delete(fn)}}
 private emit(){for(const fn of this.listeners)fn()}
 private persist(){this.options.save?.(this.reference?JSON.stringify(this.reference):null)}
 get locked(){return this.phase!=='idle'}
 get sourceSessionID(){return this.reference?.session_id??null}
 beginNavigation(){return this.locked?null:++this.generation}
 isNavigationCurrent(g:number){return !this.locked&&this.generation===g}
 get recoveryHint(){return this.reference?`${this.phase}: ${JSON.stringify(this.reference)}\n/jobs recover ${this.reference.session_id} ${this.reference.request_id} (read-only). Do not repeat with a new ID.${this.intent&&this.phase==='unknown'?' /jobs retry confirm reuses the original payload.':''}`:''}
 async create(session:string,content:string,effects:boolean,originRun=''){return this.begin({session_id:session,content,effects,origin_run:originRun,kind:'create',job_id:'',approval:'',digest:'',decision:'denied'})}
 async stop(session:string,id:string){return this.begin({session_id:session,content:'',effects:false,origin_run:'',kind:'stop',job_id:id,approval:'',digest:'',decision:'denied'})}
 async decide(session:string,id:string,approval:string,digest:string,decision:'approved'|'denied'){return this.begin({session_id:session,content:'',effects:false,origin_run:'',kind:'decision',job_id:id,approval,digest,decision})}
 private async begin(value:Omit<Intent,'request_id'>){if(this.locked)return null;this.intent=Object.freeze({...value,request_id:(this.options.id??(()=>crypto.randomUUID()))()});this.reference={session_id:this.intent.session_id,request_id:this.intent.request_id,kind:this.intent.kind,job_id:this.intent.job_id};this.phase='sending';this.result=null;this.generation++;this.emit();try{this.persist();return await this.send()}catch(e){this.failed(e);throw e}}
 private failed(e:unknown){if(e instanceof APIError&&e.code==='job_operation_rejected'){this.intent=null;this.reference=null;this.phase='idle';this.persist()}else this.phase='unknown';this.emit()}
 private async send(){const i=this.intent!;const r=i.kind==='create'?await this.client.createJob(i.session_id,i.content,i.effects,i.origin_run,i.request_id):i.kind==='stop'?await this.client.stopJob(i.session_id,i.job_id,i.request_id):await this.client.decideJob(i.session_id,i.job_id,i.approval,i.digest,i.decision,i.request_id);this.result=r;this.phase='confirmed';this.emit();return r}
 async retry(){if(this.reading||this.phase!=='unknown')return null;if(!this.intent)throw new Error('original payload not retained; read-only recovery required');this.phase='sending';this.emit();try{return await this.send()}catch(e){this.failed(e);throw e}}
 async recover(session?:string,request?:string){if(this.phase==='sending'||this.reading)return null;if(this.phase==='confirmed')return this.result;if(session&&request){if(this.reference&&(session!==this.reference.session_id||request!==this.reference.request_id))throw new Error('another job operation unresolved');if(!this.reference){this.reference={session_id:session,request_id:request,kind:'lookup',job_id:''};this.phase='unknown';this.generation++;this.persist();this.emit()}}if(!this.reference)return null;this.reading=true;try{const r=await this.client.jobOperation(this.reference.session_id,this.reference.request_id);if(this.reference.kind!=='lookup'&&(r.kind!==this.reference.kind||r.kind!=='create'&&r.job.id!==this.reference.job_id))throw new Error('job recovery reference mismatch');const i=this.intent;if(i&&(r.kind!==i.kind||r.kind==='create'&&(r.job.content!==i.content||r.job.effects!==i.effects||r.job.origin_run_id!==i.origin_run)||r.kind!=='create'&&r.job.id!==i.job_id||r.kind==='decision'&&r.payload!==JSON.stringify([i.approval,i.digest,i.decision])))throw new Error('job recovery intent mismatch');this.result=r;this.phase='confirmed';this.emit();return r}catch(e){this.phase='unknown';this.emit();throw e}finally{this.reading=false}}
 applied(session:string){if(this.phase!=='confirmed'||this.result?.session_id!==session)return;this.intent=null;this.reference=null;this.phase='idle';this.persist();this.emit()}
}
