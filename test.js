import { spawn } from 'child_process';

const p = spawn('node', ['dist/nabd.mjs'], { stdio: ['pipe', 'pipe', 'inherit'] });

const commands = [
  "hi",
  "list files in src/core",
  "read package.json",
  "delete the test.txt file",
  "refactor X in 4 steps",
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
