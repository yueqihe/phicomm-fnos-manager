// Build-time only: the NAS serves this bundle from the Go executable.
import {build} from 'esbuild';
import {copyFileSync, mkdirSync, readFileSync, readdirSync, writeFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
const root=fileURLToPath(new URL('../',import.meta.url));
const at=name => path.join(root,name);
await build({absWorkingDir:root,entryPoints:['frontend/app.jsx'],outfile:'public/app.js',bundle:true,minify:true,format:'iife',target:['es2022'],jsx:'automatic',charset:'utf8',define:{'process.env.NODE_ENV':'"production"'},legalComments:'none',logLevel:'info'});
copyFileSync(at('frontend/styles.css'),at('public/styles.css'));
mkdirSync(at('public/licenses'),{recursive:true});
const dependencies=['react','react-dom','scheduler','lucide-react'];
for(const name of dependencies) copyFileSync(at(`node_modules/${name}/LICENSE`),at(`public/licenses/${name}.LICENSE`));
const sources=['package.json','package-lock.json','scripts/build-web.mjs',...readdirSync(at('frontend')).sort().map(name => `frontend/${name}`)];
const hash=name => createHash('sha256').update(readFileSync(at(name))).digest('hex');
const assets=['public/index.html','public/app.js','public/styles.css',...dependencies.map(name => `public/licenses/${name}.LICENSE`)];
writeFileSync(at('public/build.json'),JSON.stringify({framework:'React',sources:Object.fromEntries(sources.map(name => [name,hash(name)])),assets:Object.fromEntries(assets.map(name => [name,hash(name)]))},null,2)+'\n');
