import fs from 'node:fs';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {validateProductionGraph,parseGoList} from './production-native-deps.mjs';
const files=['direct_link.go','direct_link_windows.go','directory_guard_windows.go','directory_windows.go','doc.go','entries.go','entries_windows.go','path.go','probe_windows.go','root_path.go','root_windows.go'];
const probe={ImportPath:'agent/internal/nativefs/windowsprobe',GoFiles:files};
function graph(os='windows'){return [{ImportPath:'agent/cmd/agent',Name:'main',Match:['./cmd/...'],GoFiles:['main.go']},{ImportPath:'agent/internal/interaction',GoFiles:['tools.go'],Imports:os==='windows'?[probe.ImportPath]:[]},...(os==='windows'?[structuredClone(probe)]:[])];}
test('ordinary Windows foundation only; other platforms remain disconnected',()=>{for(const os of ['windows','darwin','linux'])assert.doesNotThrow(()=>validateProductionGraph(graph(os),{GOOS:os,GOFLAGS:''}));});
test('reject tags, inherited flags, empty graph and missing command roots',()=>{
 for(const flags of ['-tags=nativeprobe_sqlite_identity','-tags=webui','-mod=mod'])assert.throws(()=>validateProductionGraph(graph(),{GOOS:'windows',GOFLAGS:flags}));
 for(const g of [[],[probe],graph().filter(p=>p!==undefined&&p.ImportPath!==probe.ImportPath)])assert.throws(()=>validateProductionGraph(g,{GOOS:'windows',GOFLAGS:''}));
});
test('reject all other nativefs and script consumers and unexpected foundation source',()=>{
 for(const path of ['agent/internal/nativefs/rootlease','agent/internal/nativefs/darwinprobe','agent/internal/nativefs/exportprobe','agent/scripts/windows-non-elevated-launcher/tokencheck','agent/internal/nativefs/windowsprobe/extra'])assert.throws(()=>validateProductionGraph([...graph(),{ImportPath:path}],{GOOS:'windows',GOFLAGS:''}));
 for(const extra of ['persistent_broker_experimental_windows.go','nativeprobe_sqlite_generated_windows.go','future.go']){const g=graph();g[2].GoFiles.push(extra);assert.throws(()=>validateProductionGraph(g,{GOOS:'windows',GOFLAGS:''}));}
 const g=graph();g[2].CgoFiles=['bridge.go'];assert.throws(()=>validateProductionGraph(g,{GOOS:'windows',GOFLAGS:''}));
 assert.throws(()=>validateProductionGraph(graph(),{GOOS:'darwin',GOFLAGS:''}));
 const direct=graph();direct[0].Imports=[probe.ImportPath];assert.throws(()=>validateProductionGraph(direct,{GOOS:'windows',GOFLAGS:''}));
});

test('unrelated upstream experimental protocol name is not a native experiment',()=>{
 assert.doesNotThrow(()=>validateProductionGraph([...graph(),{ImportPath:'github.com/chromedp/cdproto/headlessexperimental',GoFiles:['headlessexperimental.go']}],{GOOS:'windows',GOFLAGS:''}));
 assert.throws(()=>validateProductionGraph([...graph(),{ImportPath:'modernc.org/sqlite/lib',GoFiles:['nativeprobe_sqlite_generated_windows.go']}],{GOOS:'windows',GOFLAGS:''}));
});
test('go-list parser accepts concatenated objects but refuses partial, garbage and empty output',()=>{
 const g=graph();assert.deepEqual(parseGoList(g.map(p=>JSON.stringify(p,null,2)).join('\n')),g);
 for(const raw of ['', '{"ImportPath":', '{}garbage','[{}]'])assert.throws(()=>parseGoList(raw));
});

test('all migrated workflows invoke the ordinary-source checker without blanket foundation claims',()=>{
 for(const file of [".github/workflows/native-conformance.yml", ".github/workflows/native-darwin-bulk.yml", ".github/workflows/native-darwin-ownedfd.yml", ".github/workflows/native-windows-access.yml", ".github/workflows/native-windows-broker.yml", ".github/workflows/native-windows-existing-wal.yml", ".github/workflows/native-windows-lease.yml", ".github/workflows/native-windows-main-read.yml", ".github/workflows/native-windows-non-elevated.yml", ".github/workflows/native-windows-permit.yml", ".github/workflows/native-windows-private-shm-resources.yml", ".github/workflows/native-windows-private-shm-slots.yml", ".github/workflows/native-windows-private-sql-admission.yml", ".github/workflows/native-windows-private-sql-interop.yml", ".github/workflows/native-windows-private-sql-session.yml", ".github/workflows/native-windows-read-cleanup.yml", ".github/workflows/native-windows-shm-readonly.yml", ".github/workflows/native-windows-sidecar-diagnostic.yml", ".github/workflows/native-windows-stock-alias.yml", ".github/workflows/native-windows-stock-epoch.yml", ".github/workflows/native-windows-wal-flags.yml", ".github/workflows/portable-exportprobe.yml"]){
 const s=fs.readFileSync(new URL('../'+file,import.meta.url),'utf8');
 assert.ok(s.includes('node scripts/production-native-deps.mjs'),file);
 assert.ok(s.includes('node --test scripts/production-native-deps.test.mjs'),file);
 assert.ok(s.includes('Tagged graphs above are research observations, not release qualification.'),file);
 assert.ok(!s.includes("startsWith('agent/internal/nativefs/")&&!s.includes("startswith('agent/internal/nativefs/"),file);
 }
});
