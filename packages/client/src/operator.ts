import {operatorExactText} from './operator_actions.ts';
import {safeTerminalText,type AgentClient} from './index.ts';
import {operatorResources} from '@personal-agent/protocol';
import type {OperatorPage} from '@personal-agent/protocol';
export interface OperatorQuery {owner?:string;filter?:string;cursor?:string;limit?:number;signal?:AbortSignal}
export interface OperatorLocation {resource:string;owner:string;filter:string;cursor:string}
export function operatorLocation(resource:string,query:OperatorQuery={}):OperatorLocation {if(!operatorResources.includes(resource))throw Error('Unknown management resource');return {resource,owner:query.owner??'',filter:query.filter??'',cursor:query.cursor??''}}
// Read-only history with an independent generation fence. It never mutates the
// chat/controller, approval, queue, Job or context-operation navigation epochs.
export class OperatorNavigation {
 private generation=0;private abort:AbortController|undefined;private history:OperatorLocation[]=[];private index=-1;private listeners=new Set<()=>void>();
 visible=false;loading=false;error='';page:OperatorPage|undefined;
 private client:Pick<AgentClient,"operator">;readonly sessionID:string;
 constructor(client:Pick<AgentClient,"operator">,sessionID:string){this.client=client;this.sessionID=sessionID}
 get resource(){return this.history[this.index]?.resource??'projects'}
 get canBack(){return this.index>0}get canForward(){return this.index+1<this.history.length}
 subscribe(fn:()=>void){this.listeners.add(fn);return ()=>{this.listeners.delete(fn)}}
 private emit(){for(const f of this.listeners)f()}
 async open(resource:string,query:OperatorQuery={}){const location=operatorLocation(resource,query);this.history=this.history.slice(0,this.index+1);this.history.push(location);this.index++;await this.load(location)}
 async back(){if(this.canBack)await this.load(this.history[--this.index]!)}
 async forward(){if(this.canForward)await this.load(this.history[++this.index]!)}
 async next(){const current=this.history[this.index];if(current&&this.page?.next_cursor)await this.open(current.resource,{...current,cursor:this.page.next_cursor})}
 close(){this.generation++;this.abort?.abort();this.visible=false;this.loading=false;this.page=undefined;this.emit()}
 private async load(location:OperatorLocation){const generation=++this.generation;this.abort?.abort();const abort=this.abort=new AbortController();this.visible=true;this.loading=true;this.error='';this.page=undefined;this.emit();try{const page=await this.client.operator(this.sessionID,location.resource,{...location,signal:abort.signal});if(generation===this.generation)this.page=page}catch{if(generation===this.generation)this.error='Management read failed; no action was sent.'}finally{if(generation===this.generation){this.loading=false;this.emit()}}}
}
// Display cleaning never supplies identity for requests. Ordinary dotted domain
// IDs are supported exactly; separators, controls/formatting and oversized IDs
// remain unavailable for automatic drilldowns.
export function isOperatorOwnerID(id:string):boolean {return id.length<=128&&/^[a-zA-Z0-9_-]+(?:\.[a-zA-Z0-9_-]+)*$/.test(id)}
export function operatorDisplayText(value:string):string {return safeTerminalText(value).replace(/\p{Cf}/gu,'')}
export function operatorChildren(resource:string,id:string):{resource:string;owner:string}[]{if(!isOperatorOwnerID(id))return [];const targets:Record<string,string[]>={tasks:['evidence'],workflows:['nodes','checkpoints'],skills:['versions'],triggers:['trigger-state','trigger-history']};return (targets[resource]??[]).map(resource=>({resource,owner:id}))}
export function formatOperatorPage(page:OperatorPage):string {return operatorDisplayText([`${page.resource} · ${page.namespace}`,`Scope: ${page.scope_id||'none (chat-only)'}`,page.notice,...page.items.map(r=>`${page.resource==='projects'?operatorExactText(r.id):r.id} · ${page.resource==='projects'?operatorExactText(r.label):r.label} · ${r.state}\n${r.fields.map(f=>`  ${f.name}: ${f.value}`).join('\n')}`),...(page.items.length?[]:['No scoped records. Legacy data not imported.']),...page.actions.map(a=>`${a.action}: ${a.availability} · ${a.reason}`)].join('\n'))}
