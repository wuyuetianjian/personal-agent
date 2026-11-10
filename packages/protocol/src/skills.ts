import type {SkillIntent,SkillReview,SkillManifest,SkillOperation,SkillCatalogEntry,SkillCatalogPage,SkillVersionPage,SkillVersionEntry,SkillRevisionRecord} from './contracts.ts';
import {operatorRequestID,operatorNamePresent} from './operator_actions.ts';
import {knowledgeOwner} from './knowledge.ts';
export type SkillInput=Omit<SkillIntent,'request_id'>;
export const skillMaxBytes=65536;
export const skillParser='strict-manifest-v1',skillReviewer='catalog-no-execution-v1';
const hash=(v:unknown):v is string=>typeof v==='string'&&/^[a-f0-9]{64}$/.test(v);
const catalog=(v:unknown):v is string=>typeof v==='string'&&/^skill-[a-f0-9]{64}$/.test(v);
const revision=(v:unknown):v is string=>typeof v==='string'&&/^skill-rev-[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(v);
const generation=(v:unknown):v is string=>typeof v==='string'&&/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(v);
export function skillText(v:unknown,max:number):v is string{return typeof v==='string'&&new TextEncoder().encode(v).length<=max&&!v.includes('\0')&&!/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(v)}
function object(value:unknown,keys:string){if(!value||typeof value!=='object'||Array.isArray(value)||Object.keys(value).sort().join(',')!==keys.split(' ').sort().join(','))throw Error('invalid skill fields');return value as Record<string,unknown>}
function integer(v:unknown,min:number,max=Number.MAX_SAFE_INTEGER):v is number{return Number.isSafeInteger(v)&&(v as number)>=min&&(v as number)<=max}
function identity(v:unknown,max=128):v is string{return operatorRequestID(v)&&v.length<=max}
function array(v:unknown,max:number):v is unknown[]{return Array.isArray(v)&&v.length<=max}
function require(ok:unknown,reason='invalid skill evidence'):asserts ok{if(!ok)throw Error(reason)}
function inert(v:Record<string,unknown>){require(v.evaluation==='not_run'&&v.execution==='unavailable'&&v.execution_reason==='bridge_not_implemented','skill execution is unavailable')}
function profiles(v:Record<string,unknown>){require(v.parser_version===skillParser&&v.review_version===skillReviewer,'unsupported skill review profile')}
function selection(state:unknown,id:unknown,empty=false){require(state==='disabled'&&id===''||state==='enabled'&&revision(id)||empty&&state===''&&id==='','invalid skill selection')}
function manifest(value:unknown,empty=false):SkillManifest{
 const m=object(value,'id version name description status inputs requires permissions privacy budget verification workflow');
 const req=object(m.requires,'capabilities'),perm=object(m.permissions,'max_level'),privacy=object(m.privacy,'max_external_trust'),budget=object(m.budget,'max_iterations max_tool_calls'),verify=object(m.verification,'min_coverage allow_conflicts'),workflow=object(m.workflow,'nodes');
 if(empty){require(['id','version','name','description','status'].every(k=>m[k]==='')&&m.inputs===null&&req.capabilities===null&&perm.max_level===''&&privacy.max_external_trust===''&&budget.max_iterations===0&&budget.max_tool_calls===0&&verify.min_coverage===0&&verify.allow_conflicts===false&&workflow.nodes===null,'invalid empty skill review');return m as unknown as SkillManifest}
 require(identity(m.id)&&identity(m.version,64)&&skillText(m.name,256)&&operatorNamePresent(m.name)&&skillText(m.description,4096)&&['draft','active','deprecated','disabled'].includes(m.status as string));
 require(m.inputs&&typeof m.inputs==='object'&&!Array.isArray(m.inputs)&&Object.keys(m.inputs).length<=32);
 for(const [key,v] of Object.entries(m.inputs)){require(identity(key,64));const f=object(v,'type');require(['string','integer','number','boolean','object','array'].includes(f.type as string))}
 require(array(req.capabilities,64)&&req.capabilities.every(c=>identity(c))&&new Set(req.capabilities).size===req.capabilities.length);
 require(['read_only','write'].includes(perm.max_level as string)&&['local_private','local_sandboxed','trusted_remote','public_remote'].includes(privacy.max_external_trust as string));
 require(integer(budget.max_iterations,1,1000)&&integer(budget.max_tool_calls,0,10000)&&typeof verify.min_coverage==='number'&&Number.isFinite(verify.min_coverage)&&verify.min_coverage>=0&&verify.min_coverage<=1&&typeof verify.allow_conflicts==='boolean');
 require(array(workflow.nodes,64)&&workflow.nodes.length>0);
 const nodes=new Map<string,Record<string,unknown>>(),used=new Set<string>();
 for(const raw of workflow.nodes){const n=object(raw,'id capability role depends_on');require(identity(n.id,64)&&!nodes.has(n.id)&&((identity(n.capability)&&n.role==='')||(identity(n.role)&&n.capability==='')));require(n.depends_on===null||array(n.depends_on,64)&&n.depends_on.every(d=>identity(d,64))&&new Set(n.depends_on).size===n.depends_on.length);nodes.set(n.id,n);if(n.capability)used.add(n.capability as string)}
 require(used.size===req.capabilities.length&&req.capabilities.every(c=>used.has(c as string)));
 const colors=new Map<string,number>();function visit(id:string){require(nodes.has(id)&&colors.get(id)!==1,'invalid skill dependency graph');if(colors.get(id)===2)return;colors.set(id,1);for(const dep of (nodes.get(id)!.depends_on??[]) as string[])visit(dep);colors.set(id,2)}for(const id of nodes.keys())visit(id);
 return m as unknown as SkillManifest;
}
// Canonical structural comparison ignores JS object insertion order, not values.
export function skillSame(a:unknown,b:unknown):boolean{
 if(Object.is(a,b))return true;
 if(!a||!b||typeof a!=='object'||typeof b!=='object'||Array.isArray(a)!==Array.isArray(b))return false;
 const ak=Object.keys(a).sort(),bk=Object.keys(b).sort();
 return ak.length===bk.length&&ak.every((key,i)=>key===bk[i]&&skillSame((a as Record<string,unknown>)[key],(b as Record<string,unknown>)[key]));
}
export function parseSkillReview(value:unknown,allowEmpty=false):SkillReview{
 const v=object(value,'parser_version review_version artifact_sha256 content_sha256 content_bytes canonical_manifest manifest targets evaluation execution execution_reason notice');profiles(v);inert(v);
 const empty=allowEmpty&&v.artifact_sha256==='';const m=manifest(v.manifest,empty);
 require(skillText(v.notice,4096)&&array(v.targets,64));
 if(empty){require(v.content_sha256===''&&v.content_bytes===0&&v.canonical_manifest===''&&v.targets.length===0);return v as unknown as SkillReview}
 require(hash(v.artifact_sha256)&&hash(v.content_sha256)&&integer(v.content_bytes,1,skillMaxBytes)&&skillText(v.canonical_manifest,131072));
 const canonical=manifest(parseSkillJSON(v.canonical_manifest));require(skillSame(canonical,m),'canonical skill manifest differs');
 require(v.targets.length===m.workflow.nodes!.length);
 v.targets.forEach((raw,i)=>{const t=object(raw,'node_id kind id support'),n=m.workflow.nodes![i]!;require(t.node_id===n.id&&t.kind===(n.capability?'capability':'role')&&t.id===(n.capability||n.role)&&t.support==='unsupported','misleading skill target')});
 return v as unknown as SkillReview;
}
export function validateSkillInput(value:SkillInput){const v=object(value,'kind manifest_text catalog_id revision_id');if(v.kind==='import'){require(skillText(v.manifest_text,skillMaxBytes)&&v.manifest_text.trim()&&v.catalog_id===''&&v.revision_id==='','invalid skill import')}else if(v.kind==='enable'){require(v.manifest_text===''&&catalog(v.catalog_id)&&revision(v.revision_id),'exact skill revision required')}else require(v.kind==='disable'&&v.manifest_text===''&&catalog(v.catalog_id)&&v.revision_id==='','invalid skill intent')}
export function parseSkillOperation(value:unknown,session:string,request:string):SkillOperation{
 const v=object(value,'schema_version namespace operation_id session_id scope_id policy_digest kind catalog_id revision_id expected_generation expected_revision old_state old_selected_revision new_state new_selected_revision parser_version review_version review review_sha256 expires_at_ms digest state decision decision_request_id result_generation result_revision reason');
 require(v.schema_version===1&&v.namespace==='runtime-rebuild-v1'&&knowledgeOwner(session)&&v.session_id===session&&identity(request)&&identity(v.operation_id)&&(v.operation_id===request||v.decision_request_id===request),'skill operation identity mismatch');
 require(skillText(v.scope_id,128)&&hash(v.policy_digest)&&hash(v.digest)&&hash(v.review_sha256)&&catalog(v.catalog_id));profiles(v);
 require(['import','enable','disable'].includes(v.kind as string)&&integer(v.expected_revision,0)&&integer(v.result_revision,0)&&integer(v.expires_at_ms,1,8640000000000000)&&skillText(v.reason,4096));
 const absent=v.expected_revision===0;require(absent?v.expected_generation===''&&v.old_state===''&&v.old_selected_revision===''&&v.kind==='import':generation(v.expected_generation));selection(v.old_state,v.old_selected_revision,absent);selection(v.new_state,v.new_selected_revision);
 const empty=v.kind==='disable'&&v.old_state==='disabled';const r=parseSkillReview(v.review,empty);require(empty?v.revision_id===''&&r.artifact_sha256==='':revision(v.revision_id)&&r.artifact_sha256!=='');
 if(v.kind==='import')require(v.new_state===(absent?'disabled':v.old_state)&&v.new_selected_revision===v.old_selected_revision);
 if(v.kind==='enable')require(v.new_state==='enabled'&&v.new_selected_revision===v.revision_id);
 if(v.kind==='disable')require(v.new_state==='disabled'&&v.new_selected_revision===''&&v.revision_id===v.old_selected_revision);
 require(['pending','committed','no_op','rejected','expired'].includes(v.state as string));const decided=v.decision==='confirm'||v.decision==='cancel';require(decided?identity(v.decision_request_id)&&v.decision_request_id!==v.operation_id:v.decision===''&&v.decision_request_id==='');
 require(!(v.state==='pending'&&decided)&&(['pending','expired'].includes(v.state as string)||decided));
 const success=v.state==='committed'||v.state==='no_op';if(success){require(v.decision==='confirm'&&generation(v.result_generation)&&v.result_revision===(v.expected_revision as number)+(v.state==='committed'?1:0)&&(absent||v.result_generation===v.expected_generation));if(v.state==='no_op')require(!absent&&(v.kind==='import'||v.old_state===v.new_state&&v.old_selected_revision===v.new_selected_revision));}else require(v.result_generation===''&&v.result_revision===0);
 return v as unknown as SkillOperation;
}
export function parseSkillCatalog(value:unknown,id?:string):SkillCatalogEntry{const v=object(value,'id declared_id generation revision state selected_revision evaluation execution execution_reason');inert(v);require(catalog(v.id)&&(id===undefined||v.id===id)&&identity(v.declared_id)&&generation(v.generation)&&integer(v.revision,1));selection(v.state,v.selected_revision);return v as unknown as SkillCatalogEntry}
export function parseSkillVersion(value:unknown,catalogID:string):SkillVersionEntry{const v=object(value,'id catalog_id declared_version artifact_sha256 content_sha256 content_bytes parser_version review_version evaluation execution execution_reason');inert(v);profiles(v);require(catalog(catalogID)&&v.catalog_id===catalogID&&revision(v.id)&&identity(v.declared_version,64)&&hash(v.artifact_sha256)&&hash(v.content_sha256)&&integer(v.content_bytes,1,skillMaxBytes));return v as unknown as SkillVersionEntry}
export function skillPageCursor(cursor:string,session:string,catalogID=''){require(skillText(cursor,1024)&&/^[A-Za-z0-9_-]+$/.test(cursor),'invalid skill cursor');let v:unknown;try{v=JSON.parse(atob(cursor.replace(/-/g,'+').replace(/_/g,'/')))}catch{throw Error('invalid skill cursor')}require(Array.isArray(v)&&v.length===3&&v[0]===session&&v[1]===catalogID&&(catalogID?revision(v[2]):catalog(v[2]))&&btoa(JSON.stringify(v)).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'')===cursor,'skill cursor ownership mismatch');return v[2] as string}
function page(value:unknown,session:string,catalogID:string){const v=object(value,catalogID?'schema_version namespace session_id catalog_id items next_cursor':'schema_version namespace session_id items next_cursor');require(v.schema_version===1&&v.namespace==='runtime-rebuild-v1'&&knowledgeOwner(session)&&v.session_id===session&&(!catalogID||catalog(catalogID)&&v.catalog_id===catalogID)&&array(v.items,50)&&skillText(v.next_cursor,1024));let last='';for(const raw of v.items){const item=catalogID?parseSkillVersion(raw,catalogID):parseSkillCatalog(raw);require(item.id>last,'unordered skill page');last=item.id}if(v.next_cursor)require(skillPageCursor(v.next_cursor,session,catalogID)===last,'skill cursor result mismatch');return v}
export function parseSkillCatalogPage(value:unknown,session:string):SkillCatalogPage{return page(value,session,'') as unknown as SkillCatalogPage}
export function parseSkillVersionPage(value:unknown,session:string,catalogID:string):SkillVersionPage{return page(value,session,catalogID) as unknown as SkillVersionPage}
export function parseSkillRevision(value:unknown,catalogID:string,revisionID:string):SkillRevisionRecord{const v=object(value,'id catalog_id declared_version manifest_text review provenance');require(catalog(catalogID)&&revision(revisionID)&&v.id===revisionID&&v.catalog_id===catalogID&&skillText(v.manifest_text,skillMaxBytes));const r=parseSkillReview(v.review);require(v.declared_version===r.manifest.version&&new TextEncoder().encode(v.manifest_text).length===r.content_bytes);const p=object(v.provenance,'source_kind operation_id session_id scope_id imported_at_ms');require(p.source_kind==='text'&&identity(p.operation_id)&&knowledgeOwner(p.session_id)&&skillText(p.scope_id,128)&&integer(p.imported_at_ms,1,8640000000000000));return v as unknown as SkillRevisionRecord}
export function validateSkillTarget(catalogID:string,revisionID?:string){require(catalog(catalogID)&&(revisionID===undefined||revision(revisionID)),'invalid exact skill target')}
// JSON.parse alone silently chooses the last duplicate key and normalizes
// escaped unpaired surrogates. All catalog response/canonical JSON uses this
// bounded grammar reader before typed interpretation.
export function parseSkillJSON(raw:string):unknown{return strictSkillJSON(raw,false)}
// Observed native file text may contain NUL. Typed run decoding still rejects it
// in identities/arguments; catalog and caller-input parsing remain unchanged.
export function parseSkillObservedJSON(raw:string):unknown{return strictSkillJSON(raw,true)}
function strictSkillJSON(raw:string,observed:boolean):unknown{
 require(skillText(raw,4*1024*1024),'invalid skill JSON bytes');let at=0,count=0;
 const ws=()=>{while(/[\x20\t\r\n]/.test(raw[at]??'!'))at++};
 function string():string{const start=at++;while(at<raw.length){const c=raw[at++]!;if(c==='"'){const v=JSON.parse(raw.slice(start,at));require(skillText(observed?v.replaceAll('\0',''):v,4*1024*1024),'invalid skill JSON Unicode');return v}if(c==='\\')at++}throw Error('unterminated skill JSON string')}
 function value(depth:number):unknown{require(depth<=24&&++count<=10000,'skill JSON complexity bound');ws();const c=raw[at];if(c==='"')return string();if(c==='{'||c==='['){at++;ws();const out:Record<string,unknown>={},items:unknown[]=[];const end=c==='{'?'}':']';if(raw[at]===end){at++;return c==='{'?out:items}while(true){if(c==='{'){require(raw[at]==='"');const key=string();require(!Object.hasOwn(out,key),'duplicate skill JSON field');ws();require(raw[at++]===':');Object.defineProperty(out,key,{value:value(depth+1),enumerable:true,writable:true,configurable:true})}else items.push(value(depth+1));ws();if(raw[at]===end){at++;return c==='{'?out:items}require(raw[at++]===',');ws()}}
 const match=/^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/.exec(raw.slice(at));require(match,'invalid skill JSON value');at+=match[0].length;const v:unknown=JSON.parse(match[0]);require(typeof v!=='number'||Number.isFinite(v));return v}
 const result=value(0);ws();require(at===raw.length,'trailing skill JSON');return result;
}
