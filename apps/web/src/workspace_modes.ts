import type {SessionMode} from '@personal-agent/client';
// Presentation only. Unknown/stale status grants no authority: admission still
// reaches the server. Only an explicit negative produces this local warning.
export function workspaceModeRefusal(status:Record<string,unknown>,mode:SessionMode):string{
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
