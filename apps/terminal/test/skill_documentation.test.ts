import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

test('Skill current-stage bilingual documentation agrees on public execution and limits',()=>{
 const doc=readFileSync(new URL('../../../SKILL_CATALOG.md',import.meta.url),'utf8');
 const section=doc.split('## Internal deterministic read-only kernel / 内部确定性只读内核')[1]!.split('\n## ')[0]!;
 const chinese=section.split('\n').find(line=>line.startsWith('内部 Go 内核'))!;
 assert.doesNotMatch(chinese,/尚未开放.*Skill.*HTTP/);
 assert.match(chinese,/HTTP.*TypeScript.*Web.*Ink.*机器/);
 assert.match(chinese,/目录启用不会自动执行/);
 assert.match(chinese,/结构审核.*不变/);
 assert.match(section,/Hooks.*unavailable/);
 assert.match(section,/evaluation remains `not_run`/);
 assert.match(section,/readonly-workspace-v1/);
});
