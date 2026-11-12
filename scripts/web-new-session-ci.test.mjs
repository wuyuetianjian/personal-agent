import {test} from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
test('browser CI builds and embeds Web before running the entire admission regression file',()=>{
 const s=fs.readFileSync(new URL('../.github/workflows/web-new-session.yml',import.meta.url),'utf8');
 assert.match(s,/runs-on: ubuntu-24\.04/);
 assert.match(s,/pnpm install --frozen-lockfile/);
 assert.match(s,/playwright install --with-deps chromium/);
 assert.match(s,/chromium\.executablePath\(\)/);
 const build=s.indexOf('pnpm --filter @personal-agent/web build');
 const embed=s.indexOf('cp -R apps/web/dist/. internal/webui/dist/');
 const run=s.indexOf('node --test --test-reporter=tap apps/web/test/new_session.test.ts');
 assert.ok(build>=0&&embed>build&&run>embed);
 assert.ok(!s.includes('--test-name-pattern'));
 assert.match(s,/set -o pipefail/);assert.match(s,/if: \$\{\{ always\(\) \}\}/);
 assert.match(s,/contents: read/);assert.match(s,/persist-credentials: false/);
 for(const line of s.split('\n').filter(l=>l.includes('uses:')))assert.match(line,/@[0-9a-f]{40}/);
});
