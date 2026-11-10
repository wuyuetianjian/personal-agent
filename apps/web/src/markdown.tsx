import React from 'react';
import Markdown from 'react-markdown';
// Raw HTML is disabled by react-markdown; never load remote model-supplied images.
export function SafeMarkdown({children}:{children:string}){return <div className="markdown"><Markdown skipHtml components={{img:()=>null,a:({href,children})=>{let safe=false;try{const u=new URL(href??'');safe=u.protocol==='http:'||u.protocol==='https:'}catch{}return safe?<a href={href} target="_blank" rel="noopener noreferrer">{children}</a>:<>{children}</>}}}>{children}</Markdown></div>}
