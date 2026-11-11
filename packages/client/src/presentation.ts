import type {SkillOperations} from './skills.ts';
import type {NewSessionOperations} from './new_session.ts';
import {projectLabelText,displayJSON} from './new_session.ts';
import type {KnowledgeOperations} from './knowledge.ts';
import type {OperatorOperations} from './operator_actions.ts';
import {emptyState,reduceSession} from '@personal-agent/protocol';
import type {RunView,SessionState,SessionView} from '@personal-agent/protocol';
import type {AgentClient} from './index.ts';
import {JobOperations} from './job_operations.ts';
import type {JobView,JobDetail,ApprovalReview} from '@personal-agent/protocol';
import {QueueOperations} from './queue_operations.ts';
import type {QueueView} from '@personal-agent/protocol';
import type {ContextOperations} from './context_operations.ts';
import {APIError,isDefiniteMessageRejection} from './errors.ts';
// A display coordinator, not a source of business authority. No writes occur on mount.
export class SessionController {
 private admissionOperations:NewSessionOperations|undefined;
 private skillOperations:SkillOperations|undefined;
 private knowledgeOperations:KnowledgeOperations|undefined;
 private operatorOperations:OperatorOperations|undefined;
 state:SessionState; draft=''; private client:AgentClient; private sessionID:string;
 private olderRuns:RunView[]=[]; private before:number|undefined; private olderAvailable=false; private historyLoaded=false; private historyLoading=false;
 private submitting=false; private awaitingRunID=''; private uncertain=false; private uncertainRequest=''; private uncertainDraft=''; private decisions=new Set<string>(); private listeners=new Set<()=>void>();
 private jobsReadGeneration=0;private jobViewGeneration=0;
 jobs:JobView|undefined;jobDetail:JobDetail|undefined;jobReview:ApprovalReview|undefined;jobsError='';readonly jobOperations:JobOperations;
 queue:QueueView|undefined;queueError='';readonly queueOperations:QueueOperations;private readonly contextOperations:ContextOperations|undefined;
 constructor(client:AgentClient,sessionID:string,contextOperations?:ContextOperations,queueOperations?:QueueOperations,jobOperations?:JobOperations,operatorOperations?:OperatorOperations,knowledgeOperations?:KnowledgeOperations,admissionOperations?:NewSessionOperations,skillOperations?:SkillOperations){this.skillOperations=skillOperations;this.admissionOperations=admissionOperations;this.knowledgeOperations=knowledgeOperations;this.operatorOperations=operatorOperations;this.jobOperations=jobOperations??new JobOperations(client);this.queueOperations=queueOperations??new QueueOperations(client);this.contextOperations=contextOperations;this.client=client;this.sessionID=sessionID;this.state=emptyState(sessionID)}
 subscribe(fn:()=>void):()=>void{this.listeners.add(fn);return ()=>{this.listeners.delete(fn)}}
 private emit():void{for(const fn of this.listeners)fn()}
 setDraft(value:string):void{this.draft=value;this.emit()}
 get runs():RunView[]{return mergeRuns(this.olderRuns,this.state.runs)}
 get hasOlder():boolean{return this.olderAvailable}
 get loadingOlder():boolean{return this.historyLoading}
 async loadOlder():Promise<void>{if(this.historyLoading||!this.olderAvailable)return;if(this.before===undefined)throw new Error('history cursor missing');this.historyLoading=true;this.emit();try{const page=await this.client.history(this.sessionID,this.before);if(page.has_older&&page.next_before===undefined)throw new Error('history cursor missing');this.olderRuns=mergeRuns(page.runs,this.olderRuns);this.before=page.next_before;this.olderAvailable=page.has_older;this.historyLoaded=true}finally{this.historyLoading=false;this.emit()}}
 async contextCommand(name:string,args:string[]){if(!this.contextOperations)throw new Error('context operations unavailable');if(this.busy&&!this.contextOperations.locked)return {notice:'Session is busy or input is unconfirmed. Synchronize/cancel before changing context; no context write sent.'};return this.contextOperations.command(this.state,name,args)}
 // Hooks share SkillOperations quarantine. Policy management remains available during a run/approval.
 get hooksBlocked(){return this.skillBlocked}
 get skillBlocked(){return !!this.knowledgeOperations?.locked||!!this.operatorOperations?.locked||!!this.admissionOperations?.locked||this.jobOperations.locked||this.queueOperations.locked||!!this.contextOperations?.locked||this.submitting||this.uncertain||!!this.awaitingRunID||this.state.needsSnapshot}
 get knowledgeBlocked(){return !!this.operatorOperations?.locked||this.operatorBlockedWithoutKnowledge}
 get operatorBlockedWithoutKnowledge(){return !!this.skillOperations?.locked||!!this.admissionOperations?.locked||this.jobOperations.locked||this.queueOperations.locked||!!this.contextOperations?.locked||this.submitting||this.uncertain||!!this.awaitingRunID||this.state.needsSnapshot}
 get operatorBlocked(){return !!this.skillOperations?.locked||!!this.admissionOperations?.locked||!!this.knowledgeOperations?.locked||this.jobOperations.locked||this.queueOperations.locked||!!this.contextOperations?.locked||this.submitting||this.uncertain||!!this.awaitingRunID||this.state.needsSnapshot}
 get busy():boolean{return !!this.skillOperations?.locked||!!this.admissionOperations?.locked||!!this.knowledgeOperations?.locked||!!this.operatorOperations?.locked||this.jobOperations.locked||this.queueOperations.locked||!!this.queue?.items.some(q=>q.state==='pending')||!!this.contextOperations?.locked||this.submitting||this.uncertain||!!this.awaitingRunID||this.state.runs.some(r=>r.state==='running')}
 async watch(signal:AbortSignal):Promise<void>{for await(const frame of this.client.watchSession(this.sessionID,{signal})){if(frame.kind==='snapshot'){this.olderRuns=mergeRuns(this.olderRuns,this.state.runs);if(!this.historyLoaded){this.before=frame.snapshot.next_before;this.olderAvailable=frame.snapshot.has_older}}this.state=reduceSession(this.state,frame);if(this.awaitingRunID&&this.state.runs.some(r=>r.id===this.awaitingRunID))this.awaitingRunID='';this.emit();if(frame.kind==='snapshot'||frame.event.type==='run.created'||frame.event.type==='run.completed'||frame.event.type==='run.cancelled'||frame.event.type==='run.failed'||frame.event.type==='approval.waiting'||frame.event.type==='run.needs_reconciliation'){await this.refreshQueue().catch(error=>{this.queueError=String(error);this.emit()});await this.refreshJobs().catch(error=>{this.jobsError=String(error);this.emit()})}}}
 // Explicit read-only recovery: absence is not proof that an unknown write failed.
 async reconcile():Promise<void>{
  const snapshot=await this.client.snapshot(this.sessionID);
  if(snapshot.cursor>=this.state.cursor){
   this.olderRuns=mergeRuns(this.olderRuns,this.state.runs);
   this.state=reduceSession(this.state,{kind:'snapshot',snapshot});
  }
  if(this.awaitingRunID&&this.state.runs.some(r=>r.id===this.awaitingRunID))this.awaitingRunID='';
  if(this.uncertain&&snapshot.runs.some(r=>r.request_id===this.uncertainRequest)){
   this.uncertain=false;
   if(this.draft===this.uncertainDraft)this.draft='';
  }
  this.emit();
 }
 get needsReconciliation():boolean{return this.uncertain}
 async submit():Promise<void>{if(this.busy||!this.draft.trim())return;const value=this.draft;const requestID=crypto.randomUUID();this.submitting=true;this.emit();try{const ack=await this.client.submitMessage(this.sessionID,value,requestID);if(!this.state.runs.some(r=>r.id===ack.run_id))this.awaitingRunID=ack.run_id;if(this.draft===value)this.draft=''}catch(error){if(!isDefiniteMessageRejection(error)){this.uncertain=true;this.uncertainRequest=requestID;this.uncertainDraft=value}throw error}finally{this.submitting=false;this.emit()}}
 get queueBlocked():boolean{return !!this.skillOperations?.locked||!!this.admissionOperations?.locked||!!this.knowledgeOperations?.locked||!!this.operatorOperations?.locked||this.jobOperations.locked||!!this.contextOperations?.locked||this.submitting||this.uncertain||!!this.awaitingRunID||this.state.needsSnapshot||this.queueOperations.locked}
 async refreshQueue(){this.queue=await this.client.queue(this.sessionID);this.queueError='';this.emit()}
 async applyQueueResult(result:Awaited<ReturnType<QueueOperations['recover']>>){if(!result)return;if(result.session_id===this.sessionID&&result.kind==='enqueue'&&this.draft===result.item.content)this.draft='';if(result.session_id===this.sessionID)await this.refreshQueue();else await this.client.queue(result.session_id);this.queueOperations.applied(result.session_id)}
 async enqueueDraft(){if(this.queueBlocked||!this.draft.trim())return;const value=this.draft;const result=await this.queueOperations.enqueue(this.sessionID,value);if(result){if(this.draft===value)this.draft='';await this.applyQueueResult(result)}}
 async queueCommand(value:string):Promise<string>{
  const args=value.trim().split(/\s+/);const action=args[1];
  if(!action||action==='list'){await this.refreshQueue();return JSON.stringify(this.queue,null,2)}
  if(action==='recover'){const r=await this.queueOperations.recover(args[2],args[3]);await this.applyQueueResult(r);return r?`Queue ${r.kind} confirmed at source ${r.session_id}: ${r.item.id}, ${r.item.state}. No replay.`:this.queueOperations.recoveryHint}
  if(action==='retry'&&args[2]==='confirm'){const r=await this.queueOperations.retry();await this.applyQueueResult(r);return r?`Queue ${r.item.id} confirmed with original ID.`:this.queueOperations.recoveryHint}
  if(this.queueBlocked)return this.queueOperations.recoveryHint||'Input/context operation unconfirmed; no queue write sent.';
  let r;
  if(action==='add'&&/^\/queue add\s[\s\S]+$/.test(value)){const content=value.replace(/^\/queue add\s/,'');r=await this.queueOperations.enqueue(this.sessionID,content)}
  else if(action==='withdraw'&&args.length===3){r=await this.queueOperations.withdraw(this.sessionID,args[2]!)}
  else return '/queue (inspect), /queue add TEXT (explicit FIFO), /queue withdraw ITEM_ID (pending only), /queue recover [SESSION REQUEST], /queue retry confirm. /cancel stops active run, not pending items.';
  await this.applyQueueResult(r);return r?`Queue ${r.kind}: ${r.item.id}, ${r.item.state}. Immutable original policy; not live steering.`:'';
 }
 get jobsBlocked(){return !!this.skillOperations?.locked||!!this.admissionOperations?.locked||!!this.knowledgeOperations?.locked||!!this.operatorOperations?.locked||this.jobOperations.locked||this.queueOperations.locked||!!this.contextOperations?.locked||this.submitting||this.uncertain||!!this.awaitingRunID||this.state.needsSnapshot}
 async refreshJobs(signal?:AbortSignal){const generation=++this.jobsReadGeneration;const jobs=await this.client.jobs(this.sessionID,signal);if(signal?.aborted||generation!==this.jobsReadGeneration)return false;this.jobs=jobs;this.jobsError='';this.emit();return true}
 async showJob(id:string,signal?:AbortSignal){const generation=++this.jobViewGeneration;const detail=await this.client.job(this.sessionID,id,signal);const a=detail.snapshot.runs[0]?.approval;const review=a&&a.decision==='pending'?await this.client.approvalReview(a.id,signal):undefined;if(!signal?.aborted&&generation===this.jobViewGeneration){this.jobDetail=detail;this.jobReview=review;this.emit()}return detail}
 // One sequential read-only observer per mounted UI. No POST, run claim,
 // retry, origin transcript append or quarantine release occurs here.
 async watchJobs(signal:AbortSignal):Promise<void>{
  let delay=1000;while(!signal.aborted){await pauseJobObservation(signal,delay);if(signal.aborted)return;
   const displayed=this.jobDetail;const latest=displayed&&this.jobs?.items.find(j=>j.id===displayed.job.id);
   const detailPending=displayed&&latest&&(latest.state!==displayed.job.state||latest.phase!==displayed.job.phase);
   if(this.jobOperations.locked||(!detailPending&&!this.jobs?.items.some(j=>!['completed','stopped','failed'].includes(j.state))))continue;
   try{if(!await this.refreshJobs(signal)||signal.aborted)continue;const shown=this.jobDetail;const current=shown&&this.jobs?.items.find(j=>j.id===shown.job.id);if(shown&&current&&(current.state!==shown.job.state||current.phase!==shown.job.phase))await this.showJob(shown.job.id,signal);delay=1000}catch(error){if(!signal.aborted){this.jobsError=String(error);this.emit();if(error instanceof APIError&&[401,403,426].includes(error.status))return;delay=Math.min(30000,delay*2)}}
  }
 }
 async applyJobResult(r:Awaited<ReturnType<JobOperations['recover']>>){if(!r)return;if(r.session_id===this.sessionID){if(!await this.refreshJobs())throw new Error('Job confirmation inspection superseded; recover the original committed operation');if(this.jobDetail?.job.id===r.job.id)await this.showJob(r.job.id)}else await this.client.jobs(r.session_id);this.jobOperations.applied(r.session_id);return r}
 async jobsCommand(value:string):Promise<string>{
 const args=value.trim().split(/\s+/);const action=args[1];
 if(!action||action==='list'){await this.refreshJobs();return JSON.stringify(this.jobs,null,2)}
 if(action==='artifact'&&args.length===4)return 'Untrusted Job artifact (data only):\n'+JSON.stringify(await this.client.jobArtifact(this.sessionID,args[2]!,args[3]!),null,2);
 if(action==='show'&&args.length===3){const d=await this.showJob(args[2]!);return displayJSON({job:d.job,project_label:d.snapshot.session.project_label,runs:d.snapshot.runs,exact_review:this.jobReview},2)+'\nOrigin '+d.job.origin_session_id+' · Execution '+d.job.session_id+' · Job '+d.job.id+' · Run '+d.job.run_id+'\n/jobs artifact JOB_ID ARTIFACT_ID reads a bounded execution-scoped artifact. /jobs approve JOB_ID or /jobs deny JOB_ID applies only to this displayed approval. /jobs origin returns to the unchanged origin view.'}
 if(action==='origin'){this.jobViewGeneration++;this.jobDetail=undefined;this.jobReview=undefined;this.emit();return 'Origin '+this.sessionID+' unchanged. No prompt/context mutation.'}
 if(action==='recover'){const r=await this.applyJobResult(await this.jobOperations.recover(args[2],args[3]));return r?'Job operation confirmed at origin '+r.session_id+'. No replay.':this.jobOperations.recoveryHint}
 if(action==='retry'&&args[2]==='confirm'){const r=await this.applyJobResult(await this.jobOperations.retry());return r?'Job '+r.job.id+' confirmed with original ID.':this.jobOperations.recoveryHint}
 if(this.jobsBlocked)return this.jobOperations.recoveryHint||'Input/context/queue operation unresolved; no Job mutation sent.';
 let r;
 if((action==='add'||action==='edit')&&/^\/jobs (add|edit)\s[\s\S]+$/.test(value)){r=await this.jobOperations.create(this.sessionID,value.replace(/^\/jobs (add|edit)\s/,''),action==='edit',this.state.runs.at(-1)?.id??'')}
 else if(action==='stop'&&args.length===3)r=await this.jobOperations.stop(this.sessionID,args[2]!);
 else if((action==='approve'||action==='deny')&&args.length===3){const d=this.jobDetail;const a=d?.snapshot.runs[0]?.approval;if(!d||d.job.id!==args[2]||!a||a.decision!=='pending'||!this.jobReview)return 'First /jobs show JOB_ID to inspect this executing Job + Run and exact fresh approval.';r=await this.jobOperations.decide(this.sessionID,d.job.id,a.id,a.digest,action==='approve'?'approved':'denied')}
 else return '/jobs list, /jobs add TEXT (read-only), /jobs edit TEXT (explicit native edits, fresh approvals), /jobs show ID, /jobs artifact ID ARTIFACT_ID, /jobs stop ID, /jobs approve ID, /jobs deny ID, /jobs origin, /jobs recover [SESSION REQUEST], /jobs retry confirm. Exec/MCP Jobs unavailable.';
 await this.applyJobResult(r);return r?'Job '+r.kind+': '+r.job.id+' · '+r.job.state+' · origin '+r.job.origin_session_id+' · Run '+r.job.run_id:'';
 }
 approvalLocked(id:string):boolean{return !!this.skillOperations?.locked||!!this.admissionOperations?.locked||!!this.knowledgeOperations?.locked||!!this.operatorOperations?.locked||this.decisions.has(id)}
 async decide(id:string,digest:string,decision:'approved'|'denied'):Promise<void>{if(this.approvalLocked(id))return;this.decisions.add(id);this.emit();await this.client.decideApproval(id,digest,decision,crypto.randomUUID())}
}

function mergeRuns(older:RunView[],newer:RunView[]):RunView[]{const out=older.map(r=>structuredClone(r));const index=new Map(out.map((r,i)=>[r.id,i]));for(const r of newer){const at=index.get(r.id);if(at===undefined){index.set(r.id,out.length);out.push(structuredClone(r))}else out[at]=structuredClone(r)}return out}
export function mergeSessionPage(current:SessionView[],page:SessionView[]):SessionView[]{const ids=new Set(current.map(s=>s.id));return [...current,...page.filter(s=>{if(ids.has(s.id))return false;ids.add(s.id);return true})]}

export function capabilitySummary(status:Record<string,unknown>):string{
 const workspace=status['workspace'];if(!workspace||typeof workspace!=='object')return '';
 const result:string[]=[];
 const mcp=status['mcp'] as {enabled?:boolean;limits?:string}|undefined; if(mcp)result.push(mcp.enabled?'MCP 主机已配置；会话需显式启用 · stdio only · HTTP/Tasks unsupported':'MCP 默认关闭 · HTTP/Tasks unsupported');
 for(const [key,label] of [['Files','文件工具'],['Exec','命令执行']] as const){
  const value=(workspace as Record<string,unknown>)[key];
  if(value&&typeof value==='object'){
   const cap=value as Record<string,unknown>;
   if(cap['Enabled']===false)result.push(`${label}不可用：${typeof cap['Reason']==='string'?cap['Reason']:'未验证'}`);
  }
 }
 return result.join(' · ');
}

function pauseJobObservation(signal:AbortSignal,delay:number):Promise<void>{return new Promise(resolve=>{if(signal.aborted){resolve();return}const done=()=>{clearTimeout(timer);signal.removeEventListener('abort',done);resolve()};const timer=setTimeout(done,delay);signal.addEventListener('abort',done,{once:true})})}
