import type {AgentClient} from './index.ts';
import type {SessionState} from '@personal-agent/protocol';
import {APIError} from './errors.ts';
import {sessionCommand} from './session_commands.ts';
import type {ContextIntent,SessionCommandResult} from './session_commands.ts';
type Reference={session_id:string;request_id:string};
interface Options {id?:()=>string;save?:(serialized:string|null)=>void;restore?:string|null}
// Shared presentation quarantine, not execution authority. Restoring or mounting
// never POSTs. Only the authenticated Go operation ledger confirms the outcome.
export class ContextOperations {
 private intent:ContextIntent|null=null;private reference:Reference|null=null;private listeners=new Set<()=>void>();private generation=0;private reading=false;
 phase:'idle'|'sending'|'unknown'|'confirmed'='idle';result:SessionCommandResult|null=null;
 private readonly client:AgentClient;private readonly options:Options;
 constructor(client:AgentClient,options:Options={}){this.client=client;this.options=options;
  if(options.restore){const v=JSON.parse(options.restore) as ContextIntent;if(!v||typeof v.session_id!=='string'||!v.session_id||typeof v.request_id!=='string'||!v.request_id)throw new Error('invalid context recovery identity');this.reference={session_id:v.session_id,request_id:v.request_id};if('kind' in v){if(!['branch','rewind','clear','compact','original','file_undo'].includes(v.kind)||typeof v.boundary_run_id!=='string'||!Number.isSafeInteger(v.expected_cursor)||v.expected_cursor<0)throw new Error('invalid context recovery intent');this.intent={session_id:v.session_id,request_id:v.request_id,kind:v.kind,boundary_run_id:v.boundary_run_id,expected_cursor:v.expected_cursor}}this.phase='unknown'}
 }
 get sourceSessionID():string|null{return this.reference?.session_id??null}
 get pending():Readonly<ContextIntent>|null{return this.intent}
 get locked():boolean{return this.phase!=='idle'}
 get recoveryHint():string {if(!this.reference)return '';return `${this.phase}: ${JSON.stringify(this.intent??this.reference)}\nRecovery (read-only): /recover ${this.reference.session_id} ${this.reference.request_id}. Do not repeat with a new request ID.${this.intent&&this.phase==='unknown'?' Explicit same-ID retry: /retry-context confirm.':''}`}
 subscribe(fn:()=>void){this.listeners.add(fn);return ()=>{this.listeners.delete(fn)}}
 private emit(){for(const fn of this.listeners)fn()}
 private persist(){this.options.save?.(this.reference?JSON.stringify(this.intent??this.reference):null)}
 beginNavigation():number|null {if(this.locked)return null;return ++this.generation}
 isNavigationCurrent(version:number):boolean{return !this.locked&&version===this.generation}
 async command(state:SessionState,name:string,args:string[]):Promise<SessionCommandResult|null>{
  if(this.locked)return {notice:this.recoveryHint};
  let ownsWrite=false;
  try{
   const result=await sessionCommand(this.client,state,name,args,(this.options.id??(()=>crypto.randomUUID()))(),intent=>{ownsWrite=true;this.intent=Object.freeze(intent);this.reference={session_id:intent.session_id,request_id:intent.request_id};this.phase='sending';this.generation++;this.result=null;this.emit();this.persist()});
   if(ownsWrite&&this.intent){this.result=result;this.phase='confirmed';this.emit()}
   return result;
  }catch(error){if(ownsWrite)this.failed(error);throw error}
 }
 private failed(error:unknown){if(!this.intent)return;if(error instanceof APIError&&error.code==='context_operation_rejected'){this.intent=null;this.reference=null;this.phase='idle';this.persist()}else this.phase='unknown';this.emit()}
 // Explicit user command only. Never called by watch, mount, recovery or timers.
 async retry():Promise<SessionCommandResult|null>{
  if(this.reading||this.phase!=='unknown'||!this.intent)return {notice:this.recoveryHint};const i=this.intent;this.phase='sending';this.emit();
  try{let result:SessionCommandResult={notice:`Confirmed original request ${i.request_id}; explicit same-ID retry, no new intent.`};
   if(i.kind==='branch')result.session=await this.client.branchSession(i.session_id,i.boundary_run_id,i.expected_cursor,i.request_id);
   else if(i.kind==='rewind')result.session=await this.client.rewindSession(i.session_id,i.boundary_run_id,i.expected_cursor,i.request_id);
   else if(i.kind==='clear')result.session=await this.client.clearSession(i.session_id,i.expected_cursor,i.request_id);
   else if(i.kind==='file_undo'){const run=await this.client.undoEdit(i.session_id,i.boundary_run_id,i.expected_cursor,i.request_id);result.actionRunID=run.run_id;result.session=(await this.client.snapshot(i.session_id)).session;}
   else {await this.client.compactSession(i.session_id,i.expected_cursor,i.request_id,i.kind==='original'?'original':'compacted');result.session=(await this.client.snapshot(i.session_id)).session;}
   this.result=result;this.phase='confirmed';this.emit();return result;
  }catch(error){this.failed(error);throw error}
 }
 // Keep quarantine through applying a confirmed selection. React uses this on
 // the target controller's mount, not before setState's asynchronous commit.
 applied(sessionID:string):void {if(this.phase!=='confirmed'||!this.intent)return;const target=this.result?.session?.id??this.intent.session_id;if(target!==sessionID)return;this.intent=null;this.reference=null;this.phase='idle';this.persist();this.emit()}
 async recover(sessionID?:string,requestID?:string):Promise<SessionCommandResult|null>{
  if(this.phase==='sending'||this.reading)return {notice:this.recoveryHint};
  if(this.phase==='confirmed')return this.result;
  if(sessionID&&requestID){if(this.reference&&(this.reference.session_id!==sessionID||this.reference.request_id!==requestID))throw new Error('different context operation is still unresolved');this.reference={session_id:sessionID,request_id:requestID};this.phase='unknown';this.generation++;this.persist();this.emit()}
  if(!this.reference)return null;this.reading=true;
  try{
   const {session_id,request_id}=this.reference;const record=await this.client.contextOperation(session_id,request_id);
   const exact={session_id,...record.operation};if(this.intent&&JSON.stringify(this.intent)!==JSON.stringify(exact)){
    const a=this.intent,b=exact;if(a.session_id!==b.session_id||a.request_id!==b.request_id||a.kind!==b.kind||a.boundary_run_id!==b.boundary_run_id||a.expected_cursor!==b.expected_cursor)throw new Error('recovery intent mismatch');
   }
   this.intent=Object.freeze(exact);this.persist();
   const result:SessionCommandResult={notice:`Confirmed original request ${request_id}. ${record.operation.kind}; no write replay. Source ${session_id}; cursor ${record.operation.expected_cursor}; boundary ${record.operation.boundary_run_id||'empty'}.`};
   // Every recovered operation has an explicit view target. Compaction and
   // original-selection recovery must mount their source, not an unrelated
   // current controller; branch recovery still mounts its recorded child.
   if(record.action_run_id)result.actionRunID=record.action_run_id;
   result.session=(await this.client.snapshot(record.provenance?.session_id??session_id)).session;
   this.result=result;this.phase='confirmed';this.emit();return result;
  }catch(error){this.phase='unknown';this.emit();throw error}finally{this.reading=false}
 }
}
