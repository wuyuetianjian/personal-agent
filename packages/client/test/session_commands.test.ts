import {test} from 'node:test';import assert from 'node:assert/strict';
import {sessionCommand,AgentClient} from '../src/index.ts';
import {emptyState} from '@personal-agent/protocol';
test('session command requires exact boundary, clear consent and refuses ambiguous rewind without POST',async()=>{
 let calls=0;const client=new AgentClient({baseURL:'http://127.0.0.1:7319',token:()=> 't',fetch:async()=>{calls++;throw new Error('must not request')}});
 for(const [command,args] of [['branch',[]],['rewind',[]],['clear',[]]] as [string,string[]][]){const result=await sessionCommand(client,emptyState('s'),command,args,'q');assert.ok(result);assert.match(result.notice,/history|filesystem|clear confirm/)}
 assert.equal(calls,0);
});
