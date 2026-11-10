export const protocolFlavor='personal-agent-rebuild-v1' as const;
export type * from './contracts.ts';
import type { Snapshot, WireEvent, RunView, SessionView, ApprovalReview } from './contracts.ts';
export type SessionFrame = {kind:'event';event:WireEvent}|{kind:'snapshot';snapshot:Snapshot};
export interface SessionState {sessionID:string;session?:SessionView;cursor:number;runs:RunView[];needsSnapshot:boolean;hasOlder:boolean}
export const emptyState=(sessionID:string):SessionState=>({sessionID,cursor:0,runs:[],needsSnapshot:false,hasOlder:false});
const object=(x:unknown):Record<string,unknown>=>{if(!x||typeof x!=='object'||Array.isArray(x))throw new Error('invalid object');return x as Record<string,unknown>};
const str=(x:unknown):string=>{if(typeof x!=='string')throw new Error('invalid string');return x};
const sequence=(x:unknown):number=>{if(typeof x!=='number'||!Number.isSafeInteger(x)||x<0)throw new Error('invalid sequence');return x};
export function parseFrame(kind:string,json:string):SessionFrame {
 const v=object(JSON.parse(json));if(v['schema_version']!==1)throw new Error('unsupported schema version');
 if(kind==='event'){sequence(v['sequence']);str(v['session_id']);str(v['run_id']);str(v['type']);validateData(str(v['type']),object(v['data']));return {kind,event:v as unknown as WireEvent}}
 if(kind==='snapshot'){sequence(v['cursor']);const session=object(v['session']);str(session['id']);str(session['mode']);str(session['model_id']);str(session['reason']);if(session['project_label']!==undefined)parseProjectLabel(session['project_label']);if(session['provenance']!==undefined)validateProvenance(session['provenance'],str(session['id']));if(session['mcp']!==undefined)parseMCPStatus(session['mcp']);if(session['tool_capability']!==undefined)str(session['tool_capability']);if(session['exec_warning']!==undefined)str(session['exec_warning']);if(session['non_sandbox_exec']!==undefined&&typeof session['non_sandbox_exec']!=='boolean')throw new Error('invalid exec capability');if(v['next_before']!==undefined)sequence(v['next_before']);if(typeof session['files']!=='boolean'||typeof session['tools']!=='boolean'||typeof v['has_older']!=='boolean'||!Array.isArray(v['runs']))throw new Error('invalid snapshot');for(const item of v['runs']){const r=object(item);for(const k of ['id','request_id','input','state','phase','text','draft'])str(r[k]);if(!Array.isArray(r['tools']))throw new Error('invalid tools');for(const tool of r['tools']){const t=object(tool);for(const k of ['id','name','arguments','state'])str(t[k]);if(t['result']!==undefined)str(t['result']);if(t['is_error']!==undefined&&typeof t['is_error']!=='boolean')throw new Error('invalid tool error')}if(r['approval']!==undefined){const a=object(r['approval']);for(const k of ['id','digest','expires_at','decision'])str(a[k])}}return {kind,snapshot:v as unknown as Snapshot}}
 throw new Error('unknown SSE frame');
}
// Presentation projection only. Engine remains the sole business-state authority.
// No effects or command queue: mounting/reloading cannot repeat a POST.
export function reduceSession(state:SessionState,frame:SessionFrame):SessionState {
 if(frame.kind==='snapshot'){const s=frame.snapshot;if(s.schema_version!==1)throw new Error('unsupported schema');if(s.session.id!==state.sessionID)throw new Error('wrong session');return {sessionID:state.sessionID,session:s.session,cursor:s.cursor,runs:structuredClone(s.runs),needsSnapshot:false,hasOlder:s.has_older}}
 const e=frame.event;if(e.schema_version!==1)throw new Error('unsupported schema');if(e.session_id!==state.sessionID)throw new Error('wrong session');sequence(e.sequence);validateData(e.type,object(e.data));
 if(e.sequence<=state.cursor)return state;if(e.sequence!==state.cursor+1||state.needsSnapshot)return {...state,needsSnapshot:true};
 const next={...state,cursor:e.sequence,runs:structuredClone(state.runs)};const d=object(e.data);let r=next.runs.find(r=>r.id===e.run_id);
 if(e.type==='run.created'){if(!r){r={id:e.run_id,request_id:str(d['request_id']),input:str(d['input']),state:'running',phase:'queued',text:'',draft:'',tools:[]};next.runs.push(r)}return next}
 if(!r)return next;const wasCancelling=r.phase==='cancelling';
 switch(e.type){
 case 'model.started':r.phase='model';r.draft='';break;
 case 'model.text_delta':r.draft+=str(d['text']);break;
 case 'model.completed':r.text+=str(d['text']);r.draft='';r.phase='running';break;
 case 'tool.proposed':r.tools.push({id:str(d['id']),name:str(d['name']),arguments:str(d['arguments']),state:'proposed'});r.phase='running';break;
 case 'approval.waiting':r.phase='waiting_approval';r.approval={id:str(d['id']),digest:str(d['digest']),expires_at:str(d['expires_at']),decision:'pending'};break;
 case 'approval.decided':if(r.approval&&r.approval.id===d['id'])r.approval.decision=str(d['decision']);break;
 case 'tool.started':r.phase='executing';delete r.approval;if(r.tools.at(-1))r.tools.at(-1)!.state='executing';break;
 case 'tool.uncertain':case 'tool.recovered':{const t=r.tools.find(t=>t.id===d['id']);if(t){t.state=e.type==='tool.uncertain'?'unknown':'completed';if(e.type==='tool.uncertain')r.phase='needs_reconciliation';const content=str(d['content']);if(content)t.result=content;else delete t.result;if(d['is_error']===true)t.is_error=true;else delete t.is_error}break;}
 case 'tool.completed':{r.phase='running';const t=r.tools.at(-1);if(t){t.state='completed';const content=str(d['content']);if(content)t.result=content;else delete t.result;if(d['is_error']===true)t.is_error=true;else delete t.is_error}break;}
 case 'run.waiting_capacity':r.phase='waiting_capacity';break;
 case 'run.cancelling':r.phase='cancelling';break;
 case 'run.needs_reconciliation':r.phase='needs_reconciliation';break;
 case 'run.recovered':r.phase='running';r.draft='';break;
 case 'run.completed':case 'run.failed':case 'run.cancelled':r.state=str(d['state']);r.phase=r.state;delete r.approval;break;
 }
 if(wasCancelling&&r.state==='running'&&r.phase!=='needs_reconciliation')r.phase='cancelling';
 return next;
}
export const commands=[
 {name:'manage',available:true,reason:'Read-only bounded inventories; explicit metadata actions require exact preview/confirmation.'},
 {name:'knowledge',available:true,reason:'Explicit local knowledge list/search, text/file preview, exact confirm/cancel and original-ID read-only recovery; no remote fetch/export.'},
 {name:'project',available:true,reason:'Catalog only: rename/archive/restore preview; no create/use/policy. Archive is not permission revocation.'},
 {name:'op',available:true,reason:'Project rename/archive/restore and notification read metadata; exact preview/confirm/cancel and read-only recovery.'},
 {name:'help',available:true},{name:'model',available:true},{name:'status',available:true},
 {name:'context',available:true},{name:'cost',available:true,reason:'No billing attestation; estimates only'},
 {name:'permissions',available:true},{name:'history',available:true},{name:'resume',available:true},{name:'cancel',available:true},
 {name:'exec',available:false,reason:'No standalone command shortcut. NON-SANDBOXED tool needs immutable host/session opt-in and fresh exact approval; inspect session capability.'},
 {name:'branch',available:true,reason:'Explicit run boundary; immutable source, not Git'},
 {name:'recover',available:true,reason:'Read-only context-operation lookup by source session and original request ID'},
 {name:'retry-context',available:true,reason:'Explicit confirmation only; reuses frozen original intent and request ID'},
 {name:'clear',available:true,reason:'New empty context, history retained'},
 {name:'rewind',available:true,reason:'Conversation branch only; filesystem undo not implemented'},
 {name:'queue',available:true,reason:'Explicit durable FIFO: /queue add TEXT, /queue withdraw ID, /queue recover [SESSION REQUEST], /queue retry confirm. Not live steering or background Jobs'},
 {name:'jobs',available:true,reason:'Independent bounded Jobs: add/read-only, edit/native exact approvals, list/show/stop/approve/deny/origin/recover/retry. Exec/MCP unavailable'},
 {name:'hooks',available:false,reason:'Hooks are not implemented'},
 {name:'mcp',available:true,reason:'Read-only inspection; explicit host and session opt-in. stdio only; HTTP/Tasks unsupported'},
 {name:'compact',available:true,reason:'Local resolved-data omission, reversible; not semantic summarization'},
 {name:'plugins',available:false,reason:'Remote/plugin tools are unavailable in rebuilt preview'},
] as const;

function validateData(type:string,d:Record<string,unknown>):void {
 switch(type){
 case 'run.created':str(d['request_id']);str(d['input']);if(d['state']!=='running')throw new Error('invalid state');break;
 case 'run.completed':case 'run.failed':case 'run.cancelled':if(d['state']!==type.slice(4))throw new Error('invalid state');break;
 case 'model.text_delta':str(d['text']);break;
 case 'model.completed':str(d['text']);if(d['usage']!==undefined&&d['usage']!==null){const u=object(d['usage']);sequence(u['InputTokens']);sequence(u['OutputTokens'])}break;
 case 'tool.proposed':case 'tool.started':case 'approval.waiting':for(const k of ['id','name','arguments','digest','expires_at'])str(d[k]);if(typeof d['requires_approval']!=='boolean')throw new Error('invalid approval flag');break;
 case 'tool.uncertain':case 'tool.recovered':str(d['id']);str(d['call_id']);str(d['content']);if(typeof d['is_error']!=='boolean')throw new Error('invalid tool result');break;
 case 'tool.completed':str(d['content']);if(typeof d['is_error']!=='boolean')throw new Error('invalid tool result');break;
 case 'approval.decided':str(d['id']);str(d['digest']);if(d['decision']!=='approved'&&d['decision']!=='denied')throw new Error('invalid decision');break;
 }
}
// Known v1 payloads. WireEvent remains forward-compatible with unknown types.
export interface EventDataMap {
 'run.created':{request_id:string;input:string;state:'running'};
 'run.completed':{request_id:string;input:string;state:'completed'};
 'run.failed':{request_id:string;input:string;state:'failed'};
 'run.cancelled':{request_id:string;input:string;state:'cancelled'};
 'model.started':Record<string,never>;
 'model.text_delta':{text:string};
 'model.completed':{text:string;usage:{InputTokens:number;OutputTokens:number}|null};
 'tool.proposed':ProposalData;
 'approval.waiting':ProposalData;
 'tool.started':ProposalData;
 'approval.decided':{id:string;digest:string;decision:'approved'|'denied'};
 'tool.completed':{content:string;is_error:boolean};
 'tool.recovered':{id:string;call_id:string;content:string;is_error:boolean};
 'tool.uncertain':{id:string;call_id:string;content:string;is_error:boolean};
 'run.waiting_capacity':Record<string,never>;
 'run.cancelling':Record<string,never>;
 'run.recovered':Record<string,never>;
 'run.needs_reconciliation':Record<string,never>;
}
export interface ProposalData {id:string;name:string;arguments:string;requires_approval:boolean;digest:string;expires_at:string}
export type KnownWireEvent={ [K in keyof EventDataMap]:Omit<WireEvent,'type'|'data'>&{type:K;data:EventDataMap[K]} }[keyof EventDataMap];

// Review is authenticated data from Go, not model prose. Reject missing/mixed
// arms so clients cannot accidentally render exec as an edit-only approval.
export function parseApprovalReview(value:unknown):ApprovalReview {
 try {
  const v=object(value);
  if(v['kind']==='mcp' && v['exec']===undefined && v['edit']===undefined){
   const d=object(v['mcp']);for(const k of ['server_id','tool','arguments','schema_digest','config_fingerprint','protocol_version','generation','command','environment_policy','effect_class','warning','unavailable_reason'])str(d[k]);
   if(!str(d['warning']).startsWith('NON-SANDBOXED:'))throw new Error('missing non-sandbox warning');
   const args=JSON.parse(str(d['arguments']));object(args);
   return v as unknown as ApprovalReview;
  }
  if(v['kind']==='edit' && v['exec']===undefined && v['mcp']===undefined){const d=object(v['edit']);for(const k of ['scope','path','old_content','new_content','old_sha256','new_sha256'])str(d[k]);if(d['undo_of']!==undefined){for(const k of ['undo_of','source_run_id','root','root_identity','warning'])if(!str(d[k]))throw new Error('missing undo provenance')}return v as unknown as ApprovalReview}
  if(v['kind']==='exec' && v['edit']===undefined && v['mcp']===undefined){
   const d=object(v['exec']);for(const k of ['launch_mechanism','warning','scope','root_identity','program','executable_identity','executable_sha256','cwd','cwd_identity','environment_policy','termination_scope'])str(d[k]);
   if(!str(d['warning']).startsWith('NON-SANDBOXED:'))throw new Error('missing non-sandbox warning');
   for(const k of ['timeout_ms','output_limit_bytes'])if(sequence(d[k])<1)throw new Error('invalid bound');
   if(!Array.isArray(d['argv'])||!Array.isArray(d['environment'])||!Array.isArray(d['environment_changes']))throw new Error('invalid argv/environment');
   for(const arg of d['argv'])str(arg);for(const item of [...d['environment'],...d['environment_changes']]){const env=object(item);str(env['name']);str(env['value'])}
   return v as unknown as ApprovalReview;
  }
 } catch {throw new Error('invalid approval review')}
 throw new Error('invalid approval review');
}
// A proposal retained in an ended run is historical, not an actionable approval.
export function toolDisplayState(runState:string,toolState:string,isError=false):string{return runState!=='running'&&toolState==='proposed'?'未完成（回合已结束）':toolState==='completed'&&isError?'error':toolState}

export function parseMCPStatus(value:unknown):import('./contracts.ts').MCPStatus {
 const v=object(value);if(typeof v['enabled']!=='boolean'||!Array.isArray(v['servers']))throw new Error('invalid MCP status');str(v['warning']);str(v['limits']);
 for(const raw of v['servers']){const s=object(raw);for(const key of ['id','state','reason','protocol_version','config_fingerprint'])str(s[key]);sequence(s['tools'])}
 return v as unknown as import('./contracts.ts').MCPStatus;
}

function validateProvenance(value:unknown,sessionID:string):void {const p=object(value);for(const k of ['session_id','parent_session_id','boundary_run_id','kind'])str(p[k]);sequence(p['source_cursor']);if(p['session_id']!==sessionID)throw new Error('wrong provenance session')}
export function parseContextView(value:unknown,sessionID:string):import('./contracts.ts').SessionContextView {
 const v=object(value);if(v['schema_version']!==1)throw new Error('unsupported context schema');if(v['session_id']!==sessionID)throw new Error('wrong context session');if(v['selection']!=='original'&&v['selection']!=='compacted')throw new Error('invalid context selection');str(v['warning']);
 if(v['provenance']!==undefined)validateProvenance(v['provenance'],sessionID);
 if(v['version']!==undefined){const c=object(v['version']);str(c['id']);str(c['method']);sequence(c['source_cursor']);sequence(c['omitted_messages']);if(c['session_id']!==sessionID)throw new Error('wrong context session')}
 for(const k of ['original','selected']){if(!Array.isArray(v[k]))throw new Error('invalid context messages');for(const value of v[k]){const m=object(value);if(!['user','assistant','tool'].includes(str(m['role'])))throw new Error('invalid context role');str(m['content']);if(m['tool_call_id']!==undefined)str(m['tool_call_id']);if(m['tool_calls']!==undefined){if(!Array.isArray(m['tool_calls']))throw new Error('invalid calls');for(const value of m['tool_calls']){const call=object(value);str(call['id']);str(call['type']);const f=object(call['function']);str(f['name']);str(f['arguments'])}}}}
 return v as unknown as import('./contracts.ts').SessionContextView;
}

export function parseSessionOperationRecord(value:unknown,sessionID:string,requestID:string):import('./contracts.ts').SessionOperationRecord {
 const v=object(value),op=object(v['operation']);if(v['schema_version']!==1||v['session_id']!==sessionID||op['request_id']!==requestID)throw new Error('wrong operation identity');
 const kind=str(op['kind']);if(!['branch','rewind','clear','compact','original','file_undo'].includes(kind))throw new Error('invalid operation kind');str(op['boundary_run_id']);sequence(op['expected_cursor']);
 if(['branch','clear','rewind'].includes(kind)){validateProvenance(v['provenance'],str(object(v['provenance'])['session_id']));const p=object(v['provenance']);if(p['parent_session_id']!==sessionID||p['kind']!==kind||p['source_cursor']!==op['expected_cursor']||p['boundary_run_id']!==op['boundary_run_id']||v['version']!==undefined)throw new Error('wrong operation result')}
 else if(kind==='file_undo'){if(!str(v['action_run_id'])||!str(op['boundary_run_id'])||v['provenance']!==undefined||v['version']!==undefined)throw new Error('wrong undo result')}
 else {const c=object(v['version']);for(const k of ['id','session_id','method'])str(c[k]);sequence(c['source_cursor']);sequence(c['omitted_messages']);if(v['provenance']!==undefined||kind==='compact'&&c['session_id']!==sessionID)throw new Error('wrong operation result')}
 return v as unknown as import('./contracts.ts').SessionOperationRecord;
}

export function parseEditCheckpoints(value:unknown,sessionID:string):import('./contracts.ts').EditCheckpointInventory {
 const v=object(value);if(v['schema_version']!==1||v['session_id']!==sessionID)throw new Error('wrong checkpoint session/schema');if(typeof v['supported']!=='boolean'||!Array.isArray(v['checkpoints']))throw new Error('invalid checkpoint inventory');str(v['reason']);const seen=new Set<string>();
 for(const raw of v['checkpoints']){const p=object(raw);for(const k of ['id','session_id','source_run_id','scope','root','root_identity','disposition'])if(!str(p[k]))throw new Error('invalid checkpoint');if(p['session_id']!==sessionID||seen.has(str(p['id'])))throw new Error('wrong checkpoint session/duplicate');seen.add(str(p['id']));if(!['available','superseded','reserved','closed_without_undo','unknown','refused','undone'].includes(str(p['disposition'])))throw new Error('invalid disposition');if(p['undo_run_id']!==undefined)str(p['undo_run_id']);const r=parseApprovalReview({kind:'edit',edit:p['review']});if(r.kind!=='edit'||r.edit.scope!==p['scope']||r.edit.undo_of!==undefined)throw new Error('invalid source review')}
 return v as unknown as import('./contracts.ts').EditCheckpointInventory;
}

function validateQueueItem(value:unknown,sessionID:string):import('./contracts.ts').QueueItem {
 const q=object(value);for(const k of ['id','session_id','request_id','content','state','run_id','phase','blocked_reason','policy_digest'])str(q[k]);sequence(q['position']);
 if(q['session_id']!==sessionID)throw new Error('wrong queue session');
 if(!q['id']||!q['request_id']||!q['policy_digest']||!['pending','admitted','withdrawn'].includes(str(q['state'])))throw new Error('invalid queue item');
 if((q['state']==='admitted')!==!!q['run_id']||(q['state']==='pending')!==(Number(q['position'])>0))throw new Error('invalid queue claim');
 return q as unknown as import('./contracts.ts').QueueItem;
}
export function parseQueue(value:unknown,sessionID:string):import('./contracts.ts').QueueView {
 const v=object(value);if(v['schema_version']!==1)throw new Error('unsupported queue schema');if(v['session_id']!==sessionID)throw new Error('wrong queue session');if(v['limit']!==16||!Array.isArray(v['items'])||v['items'].length>66)throw new Error('invalid queue bounds');
 const ids=new Set<string>();let position=0;for(const q of v['items']){const item=validateQueueItem(q,sessionID);if(ids.has(item.id))throw new Error('duplicate queue identity');ids.add(item.id);if(item.state==='pending'&&item.position!==++position)throw new Error('invalid queue FIFO')}
 return v as unknown as import('./contracts.ts').QueueView;
}
export function parseQueueOperation(value:unknown,sessionID:string,requestID:string):import('./contracts.ts').QueueOperationRecord {
 const v=object(value);if(v['schema_version']!==1)throw new Error('unsupported queue schema');if(v['session_id']!==sessionID||v['request_id']!==requestID)throw new Error('wrong queue operation identity');if(!['enqueue','withdraw'].includes(str(v['kind'])))throw new Error('invalid queue operation');const item=validateQueueItem(v['item'],sessionID);if(v['kind']==='enqueue'&&item.request_id!==requestID||v['kind']==='withdraw'&&item.state!=='withdrawn')throw new Error('invalid queue acknowledgement');return v as unknown as import('./contracts.ts').QueueOperationRecord;
}

export function parseJob(value:unknown,origin:string):import('./contracts.ts').Job{const j=object(value);for(const key of ['id','origin_session_id','origin_run_id','session_id','run_id','request_id','content','state','phase','blocked_reason','policy_digest'])str(j[key]);if(j['origin_session_id']!==origin||!j['id']||!j['run_id']||!j['session_id']||j['session_id']===origin||typeof j['effects']!=='boolean'||!['registered','admitted','running','waiting_approval','cancelling','stopped','completed','failed','unknown'].includes(str(j['state']))||! /^[a-f0-9]{64}$/.test(str(j['policy_digest'])))throw new Error('invalid job');return j as unknown as import('./contracts.ts').Job}
export function parseJobs(value:unknown,origin:string):import('./contracts.ts').JobView{const v=object(value);if(v['schema_version']!==1||v['origin_session_id']!==origin||v['limit']!==8||!Array.isArray(v['items'])||v['items'].length>58)throw new Error('invalid jobs');const ids=new Set<string>(),runs=new Set<string>(),sessions=new Set<string>();for(const value of v['items']){const j=parseJob(value,origin);if(ids.has(j.id)||runs.has(j.run_id)||sessions.has(j.session_id))throw new Error('duplicate job execution identity');ids.add(j.id);runs.add(j.run_id);sessions.add(j.session_id)}return v as unknown as import('./contracts.ts').JobView}
export function parseJobOperation(value:unknown,origin:string,request:string):import('./contracts.ts').JobOperationRecord{const v=object(value);if(v['schema_version']!==1||v['session_id']!==origin||v['request_id']!==request||!['create','stop','decision'].includes(str(v['kind'])))throw new Error('invalid job operation');str(v['payload']);const j=parseJob(v['job'],origin);if(v['kind']==='create'&&(j.request_id!==request||JSON.stringify(JSON.parse(str(v['payload'])))!==JSON.stringify([j.content,j.origin_run_id,j.effects])))throw new Error('invalid job creation identity');return v as unknown as import('./contracts.ts').JobOperationRecord}
export function parseJobDetail(value:unknown,origin:string,id:string):import('./contracts.ts').JobDetail{const v=object(value);if(v['schema_version']!==1)throw new Error('invalid job detail');const j=parseJob(v['job'],origin);const snap=parseFrame('snapshot',JSON.stringify(v['snapshot']));if(j.id!==id||snap.kind!=='snapshot'||snap.snapshot.session.id!==j.session_id||snap.snapshot.runs.length>1||snap.snapshot.runs.some(r=>r.id!==j.run_id||r.request_id!=='job:'+j.id||r.input!==j.content))throw new Error('wrong job execution');return v as unknown as import('./contracts.ts').JobDetail}
export function parseJobArtifact(value:unknown,origin:string,jobID:string,sessionID:string,id:string):import('./contracts.ts').JobArtifact{const v=object(value);if(v['schema_version']!==1||v['origin_session_id']!==origin||v['job_id']!==jobID||v['session_id']!==sessionID||sessionID===origin||v['artifact_id']!==id||! /^[a-f0-9]{64}$/.test(id)||!['utf8','base64'].includes(str(v['encoding'])))throw new Error('invalid Job artifact identity');const size=sequence(v['bytes']),content=str(v['content']);if(size>256*1024||(v['encoding']==='utf8'?new TextEncoder().encode(content).length:atob(content).length)!==size)throw new Error('invalid Job artifact bounds');return v as unknown as import('./contracts.ts').JobArtifact}
export {parseOperatorPage,operatorSections,operatorResources} from './operator.ts';
export {parseOperatorOperation,operatorRequestID,operatorNamePresent} from './operator_actions.ts';
export type {OperatorKind} from './operator_actions.ts';
export * from './knowledge.ts';

// Descriptive provenance only: exact bounded fields, no caller policy/scope arms.
export function parseProjectLabel(value:unknown):import('./contracts.ts').ProjectLabel {
 const v=object(value),keys=['project_id','generation','revision','name'];
 if(Object.keys(v).length!==keys.length||Object.keys(v).some(k=>!keys.includes(k)))throw Error('invalid project label fields');
 for(const k of ['project_id','generation','name']){const text=str(v[k]);if(/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(text))throw Error('invalid project label Unicode')}
 if(!v['project_id']||str(v['project_id']).includes('\0')||new TextEncoder().encode(str(v['project_id'])).length>512||new TextEncoder().encode(str(v['name'])).length>2048||!/^[a-f0-9]{32}$/.test(str(v['generation']))||sequence(v['revision'])<1)throw Error('invalid project label');
 return v as unknown as import('./contracts.ts').ProjectLabel;
}
// Reuse the snapshot session validator for HTTP session acknowledgements/history.
export function parseSessionView(value:unknown):SessionView {
 const f=parseFrame('snapshot',JSON.stringify({schema_version:1,session:value,cursor:0,runs:[],has_older:false}));
 if(f.kind!=='snapshot')throw Error('invalid session');return f.snapshot.session;
}

export function parseSessionAdmission(value:unknown,requestID:string):import('./contracts.ts').SessionAdmissionView {
 const v=object(value),keys=['request_id','session_id','state','reason',...(v['mcp_opt_in']===undefined?[]:['mcp_opt_in']),...(v['project_label']===undefined?[]:['project_label'])];
 if(Object.keys(v).length!==keys.length||Object.keys(v).some(k=>!keys.includes(k)))throw Error('invalid admission fields');
 for(const key of ['request_id','session_id','state','reason'])str(v[key]);
 if(v['mcp_opt_in']!==undefined&&typeof v['mcp_opt_in']!=='boolean')throw Error('invalid admission MCP intent');
 if(new TextEncoder().encode(str(v['session_id'])).length>128||/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(str(v['session_id'])))throw Error('invalid admission session ID');
 if(v['request_id']!==requestID||!v['session_id']||!['starting','unresolved','revoked','complete'].includes(str(v['state'])))throw Error('invalid admission identity/state');
 if(v['project_label']!==undefined){if(v['state']!=='complete')throw Error('uncommitted project label');parseProjectLabel(v['project_label'])}
 return v as unknown as import('./contracts.ts').SessionAdmissionView;
}

export * from "./skills.ts";
export * from './skill_runs.ts';
