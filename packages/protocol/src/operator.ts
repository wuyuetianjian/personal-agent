import type {OperatorPage} from './contracts.ts';
export const operatorSections=[
 {resource:'tasks',label:'Tasks / 任务'},{resource:'workflows',label:'Workflows / 工作流'},
 {resource:'projects',label:'Projects / 项目'},{resource:'knowledge',label:'Knowledge / 知识'},
 {resource:'models',label:'Models / 模型'},{resource:'skills',label:'Skills / 技能'},{resource:'mcp',label:'MCP snapshots'},
 {resource:'triggers',label:'Triggers / 触发器'},{resource:'deadletters',label:'Dead letters / 死信'},{resource:'notifications',label:'Notifications / 通知'},
 {resource:'permissions',label:'Permissions / 权限'},{resource:'approvals',label:'Pending approvals / 待审批'},
 {resource:'audit',label:'Audit / 审计'},{resource:'diagnostics',label:'Diagnostics / 诊断'},
] as const;
export const operatorResources:string[]=[...operatorSections.map(x=>x.resource),'nodes','checkpoints','evidence','versions','trigger-state','trigger-history'];
const obj=(v:unknown):Record<string,unknown>=>{if(!v||typeof v!=='object'||Array.isArray(v))throw Error('invalid operator object');return v as Record<string,unknown>};
const text=(v:unknown,max=1024)=>{if(typeof v!=='string'||v.length>max)throw Error('invalid operator text');return v};
export function parseOperatorPage(value:unknown,session:string,resource:string,owner='',filter=''):OperatorPage {
 const p=obj(value);if(p['schema_version']!==1||p['namespace']!=='runtime-rebuild-v1'||p['session_id']!==session||p['resource']!==resource||p['owner']!==owner||p['filter']!==filter)throw Error('operator identity mismatch');
 text(p['scope_id']);text(p['notice'],4096);text(p['next_cursor'],2048);
 if(!Array.isArray(p['items'])||p['items'].length>50||!Array.isArray(p['actions'])||p['actions'].length>32)throw Error('operator bounds');
 for(const item of p['items']){const r=obj(item);for(const k of ['id','label','state'])text(r[k],2048);if(!Array.isArray(r['fields'])||r['fields'].length>24)throw Error('operator fields');for(const f of r['fields']){const field=obj(f);text(field['name']);text(field['value'],2048)}}
 for(const a of p['actions']){const action=obj(a);text(action['action']);text(action['reason'],2048);if(!['available','not_implemented','unsupported'].includes(text(action['availability'])))throw Error('operator availability')}
 return p as unknown as OperatorPage;
}
