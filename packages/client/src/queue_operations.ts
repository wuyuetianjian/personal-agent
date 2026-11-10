import type {AgentClient} from './index.ts';
import type {QueueOperationRecord} from '@personal-agent/protocol';
import {APIError} from './errors.ts';
type Reference={session_id:string;request_id:string;kind:'enqueue'|'withdraw'|'lookup';item_id:string};
type Intent=Reference&{kind:'enqueue'|'withdraw';content:string};
interface Options {id?:()=>string;save?:(serialized:string|null)=>void;restore?:string|null}
// Same retained-identity/unknown quarantine as ContextOperations. No queue
// execution lives in TS. Storage holds recovery metadata only, never drafts.
export class QueueOperations {
 private generation=0;private reference:Reference|null=null;private intent:Intent|null=null;private reading=false;private listeners=new Set<()=>void>();
 phase:'idle'|'sending'|'unknown'|'confirmed'='idle';result:QueueOperationRecord|null=null;
 private readonly client:AgentClient;private readonly options:Options;
 constructor(client:AgentClient,options:Options={}){this.client=client;this.options=options;if(options.restore){const v=JSON.parse(options.restore) as Reference;if(!v||!v.session_id||typeof v.session_id!=='string'||!v.request_id||typeof v.request_id!=='string'||!['enqueue','withdraw','lookup'].includes(v.kind)||typeof v.item_id!=='string')throw new Error('invalid queue recovery identity');this.reference={session_id:v.session_id,request_id:v.request_id,kind:v.kind,item_id:v.item_id};this.phase='unknown'}}
 subscribe(fn:()=>void){this.listeners.add(fn);return ()=>{this.listeners.delete(fn)}}
 private emit(){for(const fn of this.listeners)fn()}
 private persist(){this.options.save?.(this.reference?JSON.stringify(this.reference):null)}
 beginNavigation(){return this.locked?null:++this.generation}
 isNavigationCurrent(generation:number){return !this.locked&&this.generation===generation}
 get locked(){return this.phase!=='idle'}
 get sourceSessionID(){return this.reference?.session_id??null}
 get recoveryHint(){return this.reference?`${this.phase}: ${JSON.stringify(this.reference)}\n/queue recover ${this.reference.session_id} ${this.reference.request_id} (read-only). Do not repeat with a new ID.${this.intent&&this.phase==='unknown'?' /queue retry confirm reuses the exact original payload.':''}`:''}
 async enqueue(sessionID:string,content:string){return this.begin({session_id:sessionID,content,kind:'enqueue',item_id:''})}
 async withdraw(sessionID:string,itemID:string){return this.begin({session_id:sessionID,content:'',kind:'withdraw',item_id:itemID})}
 private async begin(value:Omit<Intent,'request_id'>){if(this.locked)return null;this.intent=Object.freeze({...value,request_id:(this.options.id??(()=>crypto.randomUUID()))()});const {content:_,...reference}=this.intent;this.reference=reference;this.phase='sending';this.generation++;this.result=null;this.emit();try{this.persist();return await this.send()}catch(error){this.failed(error);throw error}}
 private failed(error:unknown){if(error instanceof APIError&&error.code==='queue_operation_rejected'){this.intent=null;this.reference=null;this.phase='idle';this.persist()}else this.phase='unknown';this.emit()}
 private async send(){const i=this.intent!;const result=i.kind==='enqueue'?await this.client.enqueue(i.session_id,i.content,i.request_id):await this.client.withdrawQueue(i.session_id,i.item_id,i.request_id);this.result=result;this.phase='confirmed';this.emit();return result}
 async retry(){if(this.reading||this.phase!=='unknown')return null;if(!this.intent)throw new Error('original payload not retained after restart; use read-only recovery, never invent a replacement');this.phase='sending';this.emit();try{return await this.send()}catch(error){this.failed(error);throw error}}
 async recover(sessionID?:string,requestID?:string){
  if(this.phase==='sending'||this.reading)return null;if(this.phase==='confirmed')return this.result;
  if(sessionID&&requestID){if(this.reference&&(this.reference.session_id!==sessionID||this.reference.request_id!==requestID))throw new Error('different queue operation still unresolved');if(!this.reference){this.reference={session_id:sessionID,request_id:requestID,kind:'lookup',item_id:''};this.phase='unknown';this.generation++;this.persist();this.emit()}}
  if(!this.reference)return null;this.reading=true;
  try{const r=await this.client.queueOperation(this.reference.session_id,this.reference.request_id);if(this.reference.kind!=='lookup'&&(r.kind!==this.reference.kind||r.kind==='withdraw'&&r.item.id!==this.reference.item_id))throw new Error('queue recovery reference mismatch');if(this.intent&&(r.kind!==this.intent.kind||r.kind==='enqueue'&&r.item.content!==this.intent.content||r.kind==='withdraw'&&r.item.id!==this.intent.item_id))throw new Error('queue recovery intent mismatch');this.result=r;this.phase='confirmed';this.emit();return r}catch(error){this.phase='unknown';this.emit();throw error}finally{this.reading=false}
 }
 applied(sessionID:string){if(this.phase!=='confirmed'||this.result?.session_id!==sessionID)return;this.intent=null;this.reference=null;this.phase='idle';this.persist();this.emit()}
}
