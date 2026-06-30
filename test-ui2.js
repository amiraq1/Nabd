import { spawn } from 'child_process';

const p = spawn('node', ['dist/nabd.mjs'], { stdio: ['pipe', 'pipe', 'inherit'] });

const commands = [
  "read package.json",
  "list files in src/core",
  "/exit"
];

let out = '';
p.stdout.on('data', (d) => {
  const str = d.toString();
  process.stdout.write(str);
  out += str;
  if (str.includes('> ')) {
    if (commands.length > 0) {
      const c = commands.shift();
      p.stdin.write(c + '\n');
    }
  }
});
