import {NewSessionOperations} from '@personal-agent/client';
import type {AgentClient,NewSessionInput} from '@personal-agent/client';
import type {SessionView} from '@personal-agent/protocol';
const quote=(value:string)=>"'"+value.replaceAll("'", "'\\''")+"'";
function record(ops:NewSessionOperations,status:string,reason:string,baseURL:string){return {status,reason,request_id:ops.requestID,session_id:ops.sessionID,admission_state:ops.admission?.state??ops.phase,recovery_command:'terminal --url '+quote(baseURL)+' --admission '+quote(ops.requestID)+' --format json',recovery_args:['--url',baseURL,'--admission',ops.requestID,'--format','json'],initial_content:'not sent by this command; inspect existing session history before new input',...(ops.admission?.project_label?{project_label:ops.admission.project_label}:{})}}
export async function admitCLI(client:AgentClient,input:NewSessionInput,requestID:string,write:(s:string)=>void):Promise<{session?:SessionView;exitCode?:number}>{
 const ops=new NewSessionOperations(client,{id:()=>requestID});
 try{const session=await ops.create(input);if(session)return {session};throw Error('no admission result')}
 catch{if(!ops.locked){write(JSON.stringify({status:'rejected',reason:'admission_refused_before_claim',request_id:requestID,session_id:'',initial_content:'not sent'})+'\n');return {exitCode:2}}
  try{await ops.recover()}catch{/* Read failure is not permission to repeat creation. */}
  write(JSON.stringify(record(ops,ops.result?'admission_recovered':'needs_reconciliation',ops.result?'inspect_existing_session_before_new_input':'admission_unconfirmed',client.baseURL))+'\n');return {exitCode:4};
 }
}
export async function recoverAdmissionCLI(client:AgentClient,requestID:string,write:(s:string)=>void):Promise<number>{
 const ops=new NewSessionOperations(client,{restore:JSON.stringify({request_id:requestID})});
 try{const session=await ops.recover();write(JSON.stringify({...record(ops,session?'complete':'needs_reconciliation',session?'read_only_recovered':'admission_incomplete',client.baseURL),...(session?{session}:{})})+'\n');return session?0:4}
 catch{write(JSON.stringify(record(ops,'needs_reconciliation','admission_unconfirmed',client.baseURL))+'\n');return 4}
}
