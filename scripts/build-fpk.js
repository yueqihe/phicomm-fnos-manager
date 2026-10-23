// Optional npm entry point; the package itself is built by Go and Python.
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
const script = fileURLToPath(new URL('./build-fpk.py', import.meta.url));
const python = process.env.PYTHON || (process.platform === 'win32' ? 'py' : 'python3');
const args = process.platform === 'win32' && !process.env.PYTHON ? ['-3'] : [];
const result = spawnSync(python, [...args, script, ...process.argv.slice(2)], {stdio: 'inherit', windowsHide: true});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
