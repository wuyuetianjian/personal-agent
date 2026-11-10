import {isDefiniteMessageRejection,safeTerminalText} from '@personal-agent/client';
import type {AgentClient} from '@personal-agent/client';
import {emptyState,reduceSession} from '@personal-agent/protocol';
import type {RunView,Snapshot} from '@personal-agent/protocol';
type Format='text'|'json'|'stream-json';
type PrintIO={writeOut:(s:string)=>void;writeErr:(s:string)=>void;requestID:()=>string;drainTimeoutMs?:number};
const standardIO:PrintIO={writeOut:s=>{process.stdout.write(s)},writeErr:s=>{process.stderr.write(s)},requestID:()=>crypto.randomUUID()};
// No write is replayed. Only the original request ID is looked up after uncertainty.
export async function printRun(client:AgentClient,sessionID:string,input:string,format:Format,io:PrintIO=standardIO):Promise<number>{
 const requestID=io.requestID();const abort=new AbortController();const watcher=client.watchSession(sessionID,{signal:abort.signal});let state=emptyState(sessionID),runID='',emitted='';
 const emit=(value:unknown)=>io.writeOut(JSON.stringify(value)+'\n');
 const unknown=(reason:string)=>{const result={status:'needs_reconciliation',reason,run_id:runID,session_id:sessionID,request_id:requestID};if(format==='text')io.writeErr(`\n状态未确认 (${reason})，请核对会话 ${sessionID}、请求 ${requestID}${runID?`、运行 ${runID}`:''}。\n`);else emit(result);return 4};
 const syncText=(run:RunView):boolean=>{if(format!=='text')return true;const text=safeTerminalText(run.text+run.draft);if(!text.startsWith(emitted))return false;io.writeOut(text.slice(emitted.length));emitted=text;return true};
 const finish=(run:RunView,blocked=false):number=>{if(!syncText(run))return unknown('text_changed_after_output');if(run.phase==='needs_reconciliation')return unknown('execution_uncertain');const status=blocked&&run.state==='cancelled'?'blocked':run.state;const result={status,run_id:runID,session_id:sessionID,request_id:requestID,text:run.text+run.draft};if(format==='text'){if(status==='blocked')io.writeErr('\nblocked: 需要交互审批；取消已终止。\n');else io.writeOut('\n')}else emit(result);return status==='blocked'?3:status==='completed'?0:2};
 const match=(snapshot:Snapshot):RunView|undefined=>snapshot.runs.find(r=>r.request_id===requestID);
 const apply=(snapshot:Snapshot):RunView|undefined=>{state=reduceSession(state,{kind:'snapshot',snapshot});const run=match(snapshot);if(run)runID=run.id;return run};
 // Only approval drain is bounded. A healthy running model has no implicit deadline.
 const cancelAndDrain=async():Promise<number>=>{const drain=new AbortController();const timeout=io.drainTimeoutMs??10000;const timer=setTimeout(()=>drain.abort(),timeout);try{try{await client.cancelRun(runID,io.requestID(),drain.signal)}catch{/* cancel may already have taken effect; only observe */}while(!drain.signal.aborted){try{const run=apply(await client.snapshot(sessionID,drain.signal));if(run){if(!syncText(run))return unknown('text_changed_after_output');if(run.phase==='needs_reconciliation')return unknown('execution_uncertain');if(run.state!=='running')return finish(run,true)}}catch{/* keep bounded, read-only polling */}if(!drain.signal.aborted)await new Promise<void>(resolve=>{const done=()=>{clearTimeout(wait);drain.signal.removeEventListener('abort',done);resolve()};const wait=setTimeout(done,100);drain.signal.addEventListener('abort',done,{once:true})})}return unknown('blocked_cancel_unconfirmed')}finally{clearTimeout(timer)}};
 const inspect=async(run:RunView|undefined):Promise<number|undefined>=>{if(!run)return undefined;if(!syncText(run))return unknown('text_changed_after_output');if(run.phase==='needs_reconciliation')return unknown('execution_uncertain');if(run.state!=='running')return finish(run);if(run.phase==='waiting_approval')return cancelAndDrain();return undefined};
 const reconcile=async():Promise<RunView|undefined>=>apply(await client.snapshot(sessionID));
 try{
  try{const initial=await watcher.next();if(initial.done||!initial.value)throw new Error('initial snapshot missing');state=reduceSession(state,initial.value)}catch{const result={status:'not_sent',reason:'observer_unavailable_before_send',session_id:sessionID,request_id:requestID};if(format==='text')io.writeErr('连接不可用，消息未发送。\n');else emit(result);return 2}
  try{const ack=await client.submitMessage(sessionID,input,requestID);runID=ack.run_id}catch(error){
   if(isDefiniteMessageRejection(error)){const result={status:'rejected',session_id:sessionID,request_id:requestID,code:(error as {code:string}).code};if(format==='text')io.writeErr(`发送被拒绝：${result.code}。\n`);else emit(result);return 2}
   let found:RunView|undefined;try{found=await reconcile()}catch{return unknown('submission_unconfirmed')};if(!found)return unknown('submission_unconfirmed');const outcome=await inspect(found);if(outcome!==undefined)return outcome;
  }
  try{for await(const frame of watcher){state=reduceSession(state,frame);if(frame.kind==='event'&&frame.event.run_id===runID&&format==='stream-json')emit(frame.event);const outcome=await inspect(state.runs.find(r=>r.id===runID));if(outcome!==undefined)return outcome}}catch{/* Reconcile once, read-only, below. */}
  try{const found=await reconcile();const outcome=await inspect(found);if(outcome!==undefined)return outcome}catch{/* Preserve unknown outcome and IDs. */}
  return unknown('observer_unavailable');
 }finally{abort.abort();await watcher.return(undefined).catch(()=>{})}
}
