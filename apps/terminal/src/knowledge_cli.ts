import {APIError,formatKnowledgeOperation,knowledgeExact} from '@personal-agent/client';import type {AgentClient} from '@personal-agent/client';import {operatorRequestID,validateKnowledgeInput,knowledgeOwner} from '@personal-agent/protocol';
export interface KnowledgePrintOptions {knowledge:string;session:string;request:string;operation:string;digest:string;format:string;label:string;text:string;file:string;document:string;query:string;cursor:string;knowledgeFields:string[]}
export function validateKnowledgePrint(o:KnowledgePrintOptions){if(!knowledgeOwner(o.session)||!['json','text'].includes(o.format)||!['list','search','text','file','deactivate','reindex','confirm','cancel','lookup'].includes(o.knowledge))throw Error('knowledge requires existing session and text/json');const permitted:Record<string,string[]>={list:['cursor'],search:['query'],text:['label','text'],file:['file'],deactivate:['document'],reindex:['document'],confirm:[],cancel:[],lookup:[]};if(o.knowledgeFields.some(k=>!permitted[o.knowledge]!.includes(k)))throw Error('conflicting knowledge fields');const read=['list','search'].includes(o.knowledge);if(read){if(o.request||o.operation||o.digest)throw Error('read takes no operation IDs');if(o.knowledge==='search'&&(!o.query.trim()||new TextEncoder().encode(o.query).length>1024))throw Error('bounded query required');return}if(!operatorRequestID(o.request))throw Error('explicit stable request ID required');if(['confirm','cancel'].includes(o.knowledge)){if(!operatorRequestID(o.operation)||!/^[a-f0-9]{64}$/.test(o.digest))throw Error('exact operation ID and digest required');return}if(o.operation||o.digest)throw Error('unexpected decision fields');if(o.knowledge!=='lookup')validateKnowledgeInput(input(o))}
function input(o:KnowledgePrintOptions){return {kind:['text','file'].includes(o.knowledge)?'ingest':o.knowledge,document_id:o.document,source_kind:['text','file'].includes(o.knowledge)?o.knowledge:'',source:o.knowledge==='text'?o.label:o.file,content:o.text}}
// These are protocol-owned static codes, never raw server/error text. A refused
// request is distinct from the state of any operation already in the ledger.
const refusalReasons:Record<string,string>={
 knowledge_policy_refused:'local knowledge indexing requires a workspace in default or acceptEdits mode and exact explicit confirmation; plan and chat_only refuse',
 knowledge_native_capability_unavailable:'Required native workspace capability is unavailable; no weaker file-reader fallback is permitted.',
 knowledge_source_refused:'Workspace source was refused by the safe native reader.',
 knowledge_invalid_request:'Knowledge request fields or bounds are invalid.',
 knowledge_request_conflict:'The request conflicts with an existing intent or operation state.',
 knowledge_not_found:'No matching session-owned knowledge record was found.',
 session_not_found:'The existing session was not found.',
 unauthorized:'Authentication was refused.',
 forbidden_origin:'The loopback Host/Origin security envelope was refused.',
 invalid_request:'The request body was refused.',
 incompatible_protocol:'The rebuilt protocol header is required.',
 json_required:'JSON content type is required.',
};
export async function knowledgePrint(c:AgentClient,o:KnowledgePrintOptions,write:(s:string)=>void){
 validateKnowledgePrint(o);
 try{
  let result;
  if(o.knowledge==='list')result=await c.listKnowledge(o.session,o.cursor);
  else if(o.knowledge==='search')result=await c.searchKnowledge(o.session,o.query);
  else if(o.knowledge==='lookup')result=await c.knowledgeOperation(o.session,o.request);
  else if(o.knowledge==='confirm'||o.knowledge==='cancel')result=await c.decideKnowledge(o.session,o.operation,o.digest,o.knowledge,o.request);
  else result=await c.prepareKnowledge(o.session,input(o),o.request);
  write((o.format==='json'?JSON.stringify(result):'state' in result?formatKnowledgeOperation(result):knowledgeExact(JSON.stringify(result)))+'\n');
  return 'state' in result&&['rejected','expired'].includes(result.state)?1:0;
 }catch(e){
  const read=['list','search','lookup'].includes(o.knowledge);
  const decision=o.knowledge==='confirm'||o.knowledge==='cancel';
  const code=e instanceof APIError&&Object.hasOwn(refusalReasons,e.code)?e.code:'knowledge_outcome_unknown';
  const definite=code!=='knowledge_outcome_unknown';
  const refusedPreview=definite&&!read&&!decision;
  // A refused decision was not recorded under the attempted request ID. Inspect
  // the original operation instead; unknown transport still uses the attempted ID.
  const recoveryRequest=decision?(definite?o.operation:o.request):read?(o.knowledge==='lookup'?o.request:''):refusedPreview?'':o.request;
  const explanation=refusalReasons[code]??'The response was not confirmed; no outcome is inferred.';
  const status=read?'Read failed; no results claimed.':refusedPreview?'Preview request refused; no new preview was admitted by this attempt.':definite?'Decision request refused; the original operation outcome is not determined.':'The write outcome is unknown.';
  const recovery=recoveryRequest?' Read-only recovery: --knowledge lookup --session '+o.session+' --request-id '+recoveryRequest+'. No automatic POST retry.':'';
  const result={schema_version:1,state:refusedPreview?'refused':read||definite?'unresolved':'unknown',session_id:o.session,request_id:o.request,operation_id:o.operation||(!read&&!definite?o.request:''),code,recovery_request_id:recoveryRequest,reason:explanation+' '+status+recovery};
  write(JSON.stringify(result)+'\n');
  return refusedPreview?1:2;
 }
}
