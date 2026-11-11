// Compile the real application entry, not a CLI/SDK substitute. UI packages stay
// external and resolve through the explicitly owned fixture node_modules link.
import {createRequire} from 'node:module';import {dirname,resolve} from 'node:path';
const require=createRequire(new URL('../../web/package.json',import.meta.url));
const {buildSync}=require(require.resolve('esbuild',{paths:[dirname(require.resolve('vite/package.json'))]}));
export function buildHookCLI(outfile){buildSync({entryPoints:['apps/terminal/src/cli.tsx'],outfile,bundle:true,platform:'node',format:'esm',target:'node24',external:['ink','react','react/jsx-runtime'],logLevel:'silent'})}
if(process.argv[1]&&resolve(process.argv[1])===new URL(import.meta.url).pathname)buildHookCLI(process.argv[2]);
