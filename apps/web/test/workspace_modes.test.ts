import {test} from 'node:test';
import assert from 'node:assert/strict';
import {workspaceModeRefusal} from '../src/workspace_modes.ts';
const unavailable={workspace:{Files:{Enabled:false,Reason:'APFS gate <closed>'}}};
test('explicit Files false explains all workspace modes without changing the chosen mode',()=>{
 for(const mode of ['default','plan','acceptEdits'] as const){assert.match(workspaceModeRefusal(unavailable,mode),/APFS gate <closed>/);assert.match(workspaceModeRefusal(unavailable,mode),/仅聊天/)}
 assert.equal(workspaceModeRefusal(unavailable,'chat_only'),'');
});
test('unknown capabilities are not conclusions and recovered capability clears only the refusal',()=>{
 for(const status of [{},{workspace:null},{workspace:[]},{workspace:{Files:null}},{workspace:{Files:{}}},{workspace:{Files:{Enabled:'false'}}},{workspace:{Files:{Enabled:true}}}])assert.equal(workspaceModeRefusal(status,'plan'),'');
 assert.ok(workspaceModeRefusal(unavailable,'plan'));
 assert.equal(workspaceModeRefusal({workspace:{Files:{Enabled:true}}},'plan'),'');
 assert.match(workspaceModeRefusal({workspace:{Files:{Enabled:false}}},'default'),/未提供原因/);
});
