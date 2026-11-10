import type {AgentClient} from './index.ts';
import type {SessionState,SessionView} from '@personal-agent/protocol';
export interface ContextIntent {session_id:string;request_id:string;kind:string;boundary_run_id:string;expected_cursor:number}
export interface SessionCommandResult {notice:string;session?:SessionView;actionRunID?:string}
export const sessionCommandNames=['branch','rewind','clear','compact'] as const;
// Presentation/argument routing only. Go validates cursor, authority and policy.
export async function sessionCommand(client:AgentClient,state:SessionState,name:string,args:string[],requestID:string,begin?:(intent:ContextIntent)=>void):Promise<SessionCommandResult|null>{
 if(!sessionCommandNames.some(x=>x===name))return null;
 if(name==='rewind'&&args[0]==='files'&&args.length===1)return {notice:JSON.stringify(await client.editCheckpoints(state.sessionID),null,2)+'\n/rewind files CHECKPOINT_ID requests a NEW exact approval; not conversation rewind.'};
 if(name==='compact'&&args[0]==='view')return {notice:JSON.stringify(await client.contextView(state.sessionID),null,2)};
 if(state.needsSnapshot||state.runs.some(r=>r.state==='running'))return {notice:'Session is active or unresolved. Inspect/cancel and synchronize before changing context; no action sent.'};
 if(name==='rewind'&&args[0]==='files'){
  if(args.length!==2||!args[1])return {notice:'Usage: /rewind files (read-only inventory) or /rewind files CHECKPOINT_ID (new action and approval).'};
  begin?.({session_id:state.sessionID,request_id:requestID,kind:'file_undo',boundary_run_id:args[1],expected_cursor:state.cursor});
  const run=await client.undoEdit(state.sessionID,args[1],state.cursor,requestID);
  return {session:(await client.snapshot(state.sessionID)).session,actionRunID:run.run_id,notice:`Native file undo action ${run.run_id}. Review expected current bytes, restored bytes and provenance in the NEW approval. No model call, no conversation rewind; no exec/MCP reversal.`};
 }
 if(name==='branch'||name==='rewind'){
  if(name==='rewind'&&args[0]!=='conversation')return {notice:'Usage: /rewind conversation RUN_ID (or latest). Conversation only, not filesystem undo. Use /rewind files for native checkpoints; command/MCP/external effects are non-reversible.'};
  const boundary=args[name==='rewind'?1:0];
  if(!boundary||args.length!==(name==='rewind'?2:1))return {notice:'Usage: /branch RUN_ID (or latest). Source history is retained; only history through that explicit boundary enters the new context. Run IDs: '+state.runs.map(r=>r.id).join(', ')};
  const runID=boundary==='latest'?state.runs.at(-1)?.id:boundary;if(!runID)return {notice:'No run boundary. /clear confirm creates an empty context while retaining history.'};
  begin?.({session_id:state.sessionID,request_id:requestID,kind:name,boundary_run_id:runID,expected_cursor:state.cursor});
 const session=await (name==='branch'?client.branchSession(state.sessionID,runID,state.cursor,requestID):client.rewindSession(state.sessionID,runID,state.cursor,requestID));
  return {session,notice:`${name}: parent ${state.sessionID}, through run ${runID}, cursor ${state.cursor}. Immutable policy retained. Conversation only, no filesystem rollback and no MCP process/approval transferred. /compact view inspects inherited context.`};
 }
 if(name==='clear'){
  if(args.length!==1||args[0]!=='confirm')return {notice:'Usage: /clear confirm. Starts an empty conversation context with the same immutable policy. Old history is retained, not deleted; historical conversation inputs will not enter the new context. No filesystem rollback.'};
  begin?.({session_id:state.sessionID,request_id:requestID,kind:'clear',boundary_run_id:'',expected_cursor:state.cursor});
  return {session:await client.clearSession(state.sessionID,state.cursor,requestID),notice:'New empty conversation context. Prior history and immutable policy retained; no filesystem rollback.'};
 }
 if(args.length>1||args[0]&&args[0]!=='original')return {notice:'Usage: /compact (local resolved-data omission), /compact original (restore original selection), /compact view (inspect original vs selected). No semantic summary; original user/system authority retained.'};
 begin?.({session_id:state.sessionID,request_id:requestID,kind:args[0]==='original'?'original':'compact',boundary_run_id:'',expected_cursor:state.cursor});
 const version=await client.compactSession(state.sessionID,state.cursor,requestID,args[0]==='original'?'original':'compacted');
 return {notice:args[0]==='original'?'Original context selection restored. Transcript never changed.':`Compacted: ${version.method}; omitted ${version.omitted_messages} resolved assistant/tool messages. Original user/system authority retained. Not semantic summarization. /compact view inspects both; /compact original restores.`};
}
