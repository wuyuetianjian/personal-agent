import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import * as p from '../src/index.ts';
const fixture=JSON.parse(readFileSync(new URL('../../../protocol/v1/skills.golden.json',import.meta.url),'utf8'));
test('Go skill wire fixtures retain exact structural and zero-artifact review',()=>{
 assert.equal(typeof p.parseSkillReview,'function');
 assert.deepEqual(p.parseSkillReview(fixture.review),fixture.review);
 assert.throws(()=>p.parseSkillReview(fixture.empty_review));
 assert.deepEqual(p.parseSkillReview(fixture.empty_review,true),fixture.empty_review);
 for(const change of [{extra:true},{evaluation:'passed'},{execution:'available'},{targets:[{node_id:'explain',kind:'role',id:'reasoning',support:'supported'}]},{content_bytes:65537},{artifact_sha256:'bad'},{notice:'x'.repeat(4097)}])assert.throws(()=>p.parseSkillReview({...fixture.review,...change}));
});
test('nested manifest refuses unknown/null/Unicode/bounds/graph and misleading target evidence',()=>{
 const m=fixture.review.manifest;
 for(const change of [{extra:true},{inputs:null},{name:'\ud800'},{description:'x'.repeat(4097)},{requires:{capabilities:['undeclared']}},{permissions:{max_level:'admin'}},{budget:{max_iterations:1001,max_tool_calls:0}},{workflow:{nodes:[{id:'n',capability:'',role:'r',depends_on:['n']}]}},{verification:{min_coverage:NaN,allow_conflicts:false}}])assert.throws(()=>p.parseSkillReview({...fixture.review,manifest:{...m,...change}}));
 assert.throws(()=>p.parseSkillReview({...fixture.review,targets:[]}));
 assert.throws(()=>p.parseSkillReview({...fixture.empty_review,content_bytes:1},true));
});
test('bounded JSON refuses duplicate keys, malformed Unicode and nested resource exhaustion before shape decoding',()=>{
 assert.equal(typeof p.parseSkillJSON,'function');assert.deepEqual(p.parseSkillJSON('{"x":1}'),{x:1});
 for(const raw of ['{"x":1,"x":2}','{"x":{"a":1,"a":2}}','{"x":"\\ud800"}','["\\udfff"]','{"x":1}{}','['.repeat(25)+'0'+']'.repeat(25),'"'+ 'a'.repeat(4*1024*1024)+'"'])assert.throws(()=>p.parseSkillJSON(raw));
 assert.throws(()=>p.parseSkillReview({...fixture.review,canonical_manifest:fixture.review.canonical_manifest.replace('{','{"id":"other",')}));
});
const hash='a'.repeat(64),uuid='12345678-1234-4123-8123-123456789abc',catalogID='skill-'+hash,revisionID='skill-rev-'+uuid;
const operation=()=>({schema_version:1,namespace:'runtime-rebuild-v1',operation_id:'p',session_id:'s',scope_id:'',policy_digest:hash,kind:'import',catalog_id:catalogID,revision_id:revisionID,expected_generation:'',expected_revision:0,old_state:'',old_selected_revision:'',new_state:'disabled',new_selected_revision:'',parser_version:p.skillParser,review_version:p.skillReviewer,review:fixture.review,review_sha256:hash,expires_at_ms:2000000000000,digest:hash,state:'pending',decision:'',decision_request_id:'',result_generation:'',result_revision:0,reason:'Metadata only'});
test('skill operation decoder enforces exact state, owner, profile, selection and retained-result shapes',()=>{
 const v=operation();assert.deepEqual(p.parseSkillOperation(v,'s','p'),v);
 for(const change of [{session_id:'other'},{scope_id:'x'.repeat(129)},{operation_id:'other'},{revision_id:'latest'},{expected_revision:-1},{expected_revision:1},{result_revision:1},{state:'committed'},{decision:'confirm',decision_request_id:'p'},{new_state:'enabled'},{new_selected_revision:revisionID},{review_version:'next'},{extra:true},{review:{...fixture.review,evaluation:'pass'}},{expires_at_ms:Infinity}])assert.throws(()=>p.parseSkillOperation({...v,...change},'s','p'));
 const enabled={...v,kind:'enable',expected_generation:uuid,expected_revision:1,old_state:'disabled',new_state:'enabled',new_selected_revision:revisionID};assert.equal(p.parseSkillOperation(enabled,'s','p').revision_id,revisionID);
 assert.throws(()=>p.parseSkillOperation({...enabled,new_selected_revision:'skill-rev-12345678-1234-4123-8123-123456789abd'},'s','p'));
 assert.equal(p.parseSkillOperation({...v,state:'expired'},'s','p').state,'expired');
});
test('skill summary pages are sorted bounded resource-bound metadata, never an implicit latest artifact',()=>{
 const item={id:catalogID,declared_id:'local.lookup',generation:uuid,revision:1,state:'disabled',selected_revision:'',evaluation:'not_run',execution:'unavailable',execution_reason:'bridge_not_implemented'};
 const cursor=(s:string,c:string,id:string)=>Buffer.from(JSON.stringify([s,c,id])).toString('base64url');
 const page={schema_version:1,namespace:'runtime-rebuild-v1',session_id:'s',items:[item],next_cursor:cursor('s','',catalogID)};
 assert.equal(p.parseSkillCatalogPage(page,'s').items.length,1);
 for(const change of [{items:[item,item]},{items:Array(51).fill(item)},{next_cursor:cursor('other','',catalogID)},{next_cursor:cursor('s',catalogID,catalogID)},{items:[{...item,manifest_text:'hidden'}]},{items:[{...item,state:'enabled'}]},{items:[{...item,revision:0}]},{items:[{...item,execution:'available'}]}])assert.throws(()=>p.parseSkillCatalogPage({...page,...change},'s'));
 const version={id:revisionID,catalog_id:catalogID,declared_version:'1.0',artifact_sha256:hash,content_sha256:hash,content_bytes:1,parser_version:p.skillParser,review_version:p.skillReviewer,evaluation:'not_run',execution:'unavailable',execution_reason:'bridge_not_implemented'};
 const versions={...page,catalog_id:catalogID,items:[version],next_cursor:cursor('s',catalogID,revisionID)};
 assert.equal(p.parseSkillVersionPage(versions,'s',catalogID).items.length,1);assert.throws(()=>p.parseSkillVersionPage(versions,'s','skill-'+'b'.repeat(64)));
 assert.throws(()=>p.parseSkillVersionPage({...versions,items:[{...version,declared_version:'latest/path'}]},'s',catalogID));
});
test('exact canonical comparison does not normalize negative zero into zero',()=>{const negative=fixture.numeric_reviews[0].review;assert.ok(Object.is(negative.manifest.verification.min_coverage,-0));assert.throws(()=>p.parseSkillReview({...negative,manifest:{...negative.manifest,verification:{...negative.manifest.verification,min_coverage:0}}}));assert.equal(p.skillSame(-0,0),false)});
