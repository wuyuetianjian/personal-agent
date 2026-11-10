import {test} from 'node:test';import assert from 'node:assert/strict';import {parseTerminalMarkdown} from '../src/markdown.ts';
test('terminal markdown identifies headings and fenced code without interpreting terminal controls',()=>{const lines=parseTerminalMarkdown('# 标题\n```ts\nconst x = 1\n```\n**重点**');assert.equal(lines[0]?.kind,'heading');assert.equal(lines[1]?.kind,'code');assert.equal(lines[1]?.text,'const x = 1');assert.equal(lines[2]?.kind,'paragraph');assert.equal(lines[2]?.text,'**重点**')});
test('terminal markdown supports lists, emphasis, inline code and safe links as inert text',()=>{
 const lines=parseTerminalMarkdown('- **粗体** 和 *斜体* 与 `code` [文档](https://example.test/a)\n1. [危险](javascript:alert)\n\u001b]52;c;secret\u0007text');
 assert.equal(lines[0]?.kind,'list');assert.deepEqual(lines[0]?.segments.map(s=>s.style),['plain','bold','plain','italic','plain','code','plain','link']);assert.equal(lines[0]?.segments.at(-1)?.text,'文档 (https://example.test/a)');assert.equal(lines[1]?.kind,'list');assert.ok(!JSON.stringify(lines).includes('\u001b'));assert.ok(!JSON.stringify(lines).includes('secret'));assert.ok(!lines[1]?.segments.some(s=>s.style==='link'));
});
