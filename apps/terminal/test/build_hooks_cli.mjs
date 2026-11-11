// Compile the real application entry, not a CLI/SDK substitute. UI packages stay
// external and resolve from the terminal package, without cross-volume links.
import {mkdtempSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';import {dirname,resolve} from 'node:path';
const require=createRequire(new URL('../../web/package.json',import.meta.url));
const {buildSync}=require(require.resolve('esbuild',{paths:[dirname(require.resolve('vite/package.json'))]}));
// Keep generated modules under their owning package so pnpm's relative dependency
// graph resolves on Windows even when the OS temp directory is on another drive.
export function createHookCLIDirectory(){return mkdtempSync(fileURLToPath(new URL('../.hooks-cli-',import.meta.url)))}
export function buildHookCLI(outfile){buildSync({entryPoints:['apps/terminal/src/cli.tsx'],outfile,bundle:true,platform:'node',format:'esm',target:'node24',external:['ink','react','react/jsx-runtime'],logLevel:'silent'})}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url))buildHookCLI(process.argv[2]);
