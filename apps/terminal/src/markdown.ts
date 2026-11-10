import {safeTerminalText} from '@personal-agent/client';
type Style='plain'|'bold'|'italic'|'code'|'link';
export interface MarkdownLine {kind:'heading'|'code'|'paragraph'|'list';text:string;segments:Array<{text:string;style:Style}>}
function inline(text:string):MarkdownLine['segments']{
 const out:MarkdownLine['segments']=[];const pattern=/\*\*([^*]+)\*\*|\*([^*]+)\*|_([^_]+)_|`([^`]+)`|\[([^\]]+)\]\(([^)]+)\)/g;let offset=0;
 for(const m of text.matchAll(pattern)){const at=m.index;if(at>offset)out.push({text:text.slice(offset,at),style:'plain'});if(m[1]!==undefined)out.push({text:m[1],style:'bold'});else if(m[2]!==undefined||m[3]!==undefined)out.push({text:m[2]??m[3]??'',style:'italic'});else if(m[4]!==undefined)out.push({text:m[4],style:'code'});else {const label=m[5]??'',url=m[6]??'';let safe=false;try{const u=new URL(url);safe=(u.protocol==='http:'||u.protocol==='https:')&&!u.username&&!u.password}catch{}out.push({text:safe?`${label} (${url})`:label,style:safe?'link':'plain'})}offset=at+m[0].length}
 if(offset<text.length)out.push({text:text.slice(offset),style:'plain'});return out;
}
export function parseTerminalMarkdown(input:string):MarkdownLine[]{
 const lines:MarkdownLine[]=[];let fenced=false;
 for(const line of safeTerminalText(input).split('\n')){if(/^\s*```/.test(line)){fenced=!fenced;continue}const heading=!fenced?/^#{1,6}\s+(.*)$/.exec(line):null;const list=!fenced&&!heading?/^\s*(?:([-+*])|(\d+)[.)])\s+(.*)$/.exec(line):null;const text=heading?.[1]??(list?`${list[2]?list[2]+'.':'•'} ${list[3]??''}`:line);lines.push({kind:fenced?'code':heading?'heading':list?'list':'paragraph',text,segments:fenced?[{text,style:'code'}]:inline(text)})}return lines;
}
