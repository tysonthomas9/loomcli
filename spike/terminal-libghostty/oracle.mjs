import fs from 'node:fs';
import headless from '@xterm/headless';
const { Terminal } = headless;

const args = {};
for (let i = 2; i < process.argv.length; i += 2) args[process.argv[i].slice(2)] = process.argv[i + 1];
const stream = fs.readFileSync(args.stream), snapshot = fs.readFileSync(args.snapshot);
const cut = Number(args.cut), cols = Number(args.cols), rows = Number(args.rows);

function state(t) {
  const b = t.buffer.active, cells = [];
  for (let y = 0; y < rows; y++) {
    const line = b.getLine(y), row = [];
    for (let x = 0; x < cols; x++) {
      const c = line?.getCell(x);
      row.push(c ? { chars:c.getChars(), width:c.getWidth(), fg:c.getFgColor(), fgm:(c.getFgColorMode() === 16777216 || c.getFgColorMode() === 33554432) ? 1 : c.getFgColorMode(), bg:c.getBgColor(), bgm:(c.getBgColorMode() === 16777216 || c.getBgColorMode() === 33554432) ? 1 : c.getBgColorMode(), bold:c.isBold(), dim:c.isDim(), italic:c.isItalic(), underline:c.isUnderline(), inverse:c.isInverse(), blink:c.isBlink(), invisible:c.isInvisible(), strike:c.isStrikethrough() } : null);
    }
    cells.push(row);
  }
  return { cols, rows, active:b.type, cursor:{x:b.cursorX,y:b.cursorY}, cells };
}
async function make(data) { const t = new Terminal({ cols, rows, allowProposedApi:true }); await new Promise(resolve => t.write(data, resolve)); return t; }
const a = await make(stream), b = await make(Buffer.concat([snapshot, stream.subarray(cut)]));
const sa = state(a), sb = state(b);
if (JSON.stringify(sa) !== JSON.stringify(sb)) {
  let diff = 'state differs';
  outer: for (let y=0;y<rows;y++) for (let x=0;x<cols;x++) if (JSON.stringify(sa.cells[y][x]) !== JSON.stringify(sb.cells[y][x])) { diff=`cell (${x},${y}) A=${JSON.stringify(sa.cells[y][x])} B=${JSON.stringify(sb.cells[y][x])}`; break outer; }
  console.error(JSON.stringify({ pass:false, diff, cursorA:sa.cursor, cursorB:sb.cursor, activeA:sa.active, activeB:sb.active })); process.exit(1);
}
console.log(JSON.stringify({ pass:true }));
