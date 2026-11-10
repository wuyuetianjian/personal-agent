import type {OperatorOperation} from './contracts.ts';
export type OperatorKind='project.create'|'project.rename'|'project.archive'|'project.restore'|'notification.mark-read';
// Match Go unicode.IsSpace (strings.TrimSpace), not JavaScript trim: NEL is
// whitespace; BOM is an exact formatting character and is visibly escaped.
export function operatorNamePresent(v:string):boolean{return /[^\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]/u.test(v)}
export function operatorRequestID(v:unknown):v is string{return typeof v==='string'&&v!=='.'&&v!=='..'&&/^[a-zA-Z0-9_.-]{1,128}$/.test(v)}
export function parseOperatorOperation(value:unknown,session:string,request:string):OperatorOperation{
 if(!value||typeof value!=='object'||Array.isArray(value))throw Error('invalid operator operation');const v=value as Record<string,unknown>;
 const strings=[...(v.kind==='project.create'?['execution_class']:[]),'namespace','operation_id','session_id','scope_id','policy_digest','kind','target_id','old_value','new_value','revision','digest','state','decision','decision_request_id','reason'];
 if(Object.keys(v).length!==strings.length+2||strings.some(k=>typeof v[k]!=='string'||/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(v[k] as string)||new TextEncoder().encode(v[k] as string).length>4096))throw Error('invalid operator fields');
 if(v.schema_version!==1||v.namespace!=='runtime-rebuild-v1'||v.session_id!==session||!operatorRequestID(request)||!operatorRequestID(v.operation_id)||(v.operation_id!==request&&v.decision_request_id!==request))throw Error('operator identity mismatch');
 for(const k of ['digest','revision','policy_digest'])if(!/^[a-f0-9]{64}$/.test(v[k] as string))throw Error('invalid operator digest');
 if(!Number.isSafeInteger(v.expires_at_ms)||(v.expires_at_ms as number)<=0||!['project.create','project.rename','project.archive','project.restore','notification.mark-read'].includes(v.kind as string)||!['pending','committed','no_op','rejected','expired'].includes(v.state as string))throw Error('invalid operator state');
 if(!(v.target_id as string).length||new TextEncoder().encode(v.target_id as string).length>512||(v.target_id as string).includes('\0')||new TextEncoder().encode(v.new_value as string).length>2048||new TextEncoder().encode(v.old_value as string).length>2048)throw Error('invalid operator target');
 if(v.kind==='notification.mark-read'&&(v.new_value!=='read'||!['unread','read'].includes(v.old_value as string))||v.kind==='project.rename'&&!(v.new_value as string).trim())throw Error('invalid operator intent');
 if((v.kind==='project.archive'||v.kind==='project.restore')&&(!['active','archived'].includes(v.old_value as string)||v.new_value!==(v.kind==='project.archive'?'archived':'active')))throw Error('invalid catalog state');
 if(v.kind==='project.create'&&(v.execution_class!=='metadata_only'||!/^project-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v.target_id as string)||v.old_value!==''||!operatorNamePresent(v.new_value as string)||v.state==='no_op'))throw Error('invalid project creation');
 const decided=v.decision==='confirm'||v.decision==='cancel';
 if(decided?!operatorRequestID(v.decision_request_id):v.decision!==''||v.decision_request_id!=='')throw Error('invalid operator decision');
 if(v.state==='pending'&&decided||!['pending','expired'].includes(v.state as string)&&!decided||['committed','no_op'].includes(v.state as string)&&v.decision!=='confirm'||v.state==='no_op'&&v.old_value!==v.new_value||v.state==='committed'&&v.old_value===v.new_value)throw Error('inconsistent operator outcome');
 return v as unknown as OperatorOperation;
}
