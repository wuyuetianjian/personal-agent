import type {SessionMode} from '@personal-agent/client';
// Presentation only. Unknown/stale status grants no authority: admission still
// reaches the server. Only an explicit negative produces this local warning.
export function workspaceModeRefusal(status:Record<string,unknown>,mode:SessionMode,trusted=false):string{
 if(trusted){if(!['default','plan'].includes(mode))return '受信工作区仅支持 default/plan 的 Read/Search；不支持仅聊天或编辑模式。已保留选择，未提交。';if(!trustedWorkspaceAvailable(status))return '服务端未启用受信工作区，未提交。请保留原模式或明确取消受信选项。';return '';}
 if(mode==='chat_only')return '';
 const workspace=status['workspace'];
 if(!workspace||typeof workspace!=='object'||Array.isArray(workspace))return '';
 const files=(workspace as Record<string,unknown>)['Files'];
 if(!files||typeof files!=='object'||Array.isArray(files))return '';
 const cap=files as Record<string,unknown>;
 if(cap['Enabled']!==false)return '';
 const reason=typeof cap['Reason']==='string'&&cap['Reason']?cap['Reason']:'服务端未提供原因';
 return `此模式需要工作区，当前文件工具不可用：${reason}。未提交创建请求，已保留所选模式；可明确选择“仅聊天”。能力状态仅供提示，实际权限由服务端决定。`;
}

export function trustedWorkspaceAvailable(status:Record<string,unknown>):boolean {const value=status['trusted_workspace'];return !!value&&typeof value==='object'&&!Array.isArray(value)&&(value as Record<string,unknown>)['Enabled']===true}
export const trustedWorkspaceWarning='仅 Read/Search；不支持 Apply/Undo/Exec/MCP。不防外部恶意替换，不识别全部历史数据库副本；Windows 非管理员尚待验收。仅用于你明确选择并信任的工作区。';
