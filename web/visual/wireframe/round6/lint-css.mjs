// Holds the wireframe's stylesheets to the tokens.
//   node lint-css.mjs
// base.css and components.css are what the two clients will be built from, so in them every colour, length, duration
// and font comes from a variable that ../../system/tokens.css defines. wire.css draws only the wireframe's own chrome
// (the bar, the board, the phone's bezel): it may set its own lengths, and is still held to the tokens for colour.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (rel) => fs.readFileSync(path.join(here, rel), 'utf8');

const tokens = JSON.parse(read('../../system/tokens.json'));
const breakpoints = new Set([tokens.layout.breakpointPhone, tokens.layout.breakpointNarrow]);
const defined = new Set([...read('../../system/tokens.css').matchAll(/(--o-[a-z0-9-]+)\s*:/g)].map((m) => m[1]));

// What a strict sheet may not hold, each with the reason printed beside the line.
const STRICT = [
  [/#[0-9a-fA-F]{3,8}\b/, 'a hex colour: name a role, an ink, a surface or a line'],
  [/\b(rgb|rgba|hsl|hsla)\(/, 'a colour function: name a token'],
  [/(?<![\w-])-?\d*\.?\d+px\b/, 'a px length: use a space, size, radius or layout token'],
  [/(?<![\w-])\d*\.?\d+r?em\b/, 'an em length: use a token'],
  [/(?<![\w-])\d*\.?\d+m?s\b/, 'a duration: use a move or loop token'],
  [/\bfont-size\s*:/, 'font-size: set font to a type token'],
  [/\bfont-family\s*:(?!\s*"Orrery Mono")/, 'font-family: set font to a type token'],
  [/\bline-height\s*:/, 'line-height: the type token carries it'],
  [/\bfont-weight\s*:(?!\s*var\()/, 'a raw font-weight: use --o-font-weight-*'],
  [/\bletter-spacing\s*:(?!\s*(var\(|0\b|normal))/, 'raw letter-spacing: use the label\'s tracking'],
  [/\bopacity\s*:\s*0?\.\d/, 'dimming with opacity: draw it in a lower ink instead'],
  [/\bz-index\s*:(?!\s*var\()/, 'a raw z-index: use a layer token'],
];
const COLOUR_ONLY = STRICT.slice(0, 2);
const NAMED = /:\s*(white|black|red|green|blue|yellow|orange|purple|grey|gray|silver)\b/;

const strip = (css) => css.replace(/\/\*[\s\S]*?\*\//g, (c) => c.replace(/[^\n]/g, ' '));

let failures = 0;
const fail = (file, line, text, why) => { failures += 1; console.error(`${file}:${line}: ${why}\n    ${text.trim().slice(0, 160)}`); };

const check = (file, rules) => {
  const lines = strip(read(file)).split('\n');
  lines.forEach((text, i) => {
    // The face is declared once, in base.css; a keyframe's percentages and a face's weight are not lengths or tokens.
    if (/^\s*@font-face/.test(text)) return;
    // A media query cannot read a variable, so it names a breakpoint token's number, or one less for max-width.
    if (/^\s*@media/.test(text)) {
      for (const [, n] of text.matchAll(/(\d+)px/g)) if (!breakpoints.has(Number(n)) && !breakpoints.has(Number(n) + 1)) fail(file, i + 1, text, `${n}px is not a breakpoint token`);
      return;
    }
    for (const [pattern, why] of rules) if (pattern.test(text)) fail(file, i + 1, text, why);
    if (NAMED.test(text)) fail(file, i + 1, text, 'a named colour: name a token');
    for (const [, name] of text.matchAll(/var\((--o-[a-z0-9-]+)/g)) {
      if (!defined.has(name)) fail(file, i + 1, text, `${name} is not a token`);
    }
    for (const [, name, fallback] of text.matchAll(/var\((--(?!o-)[a-z0-9-]+)(\s*,)?/g)) {
      // A component may pass a token to its parts through a local variable. It must be set in this sheet, or be
      // read with a fallback (the markup sets it).
      if (!fallback && !new RegExp(`${name}\\s*:`).test(lines.join('\n'))) fail(file, i + 1, text, `${name} is read and never set`);
    }
  });
};

check('css/base.css', STRICT);
check('css/components.css', STRICT);
check('css/wire.css', COLOUR_ONLY);

// The scripts draw with classes. None of them names a colour or a length; mark.js is the logo and is exempt.
for (const file of fs.readdirSync(path.join(here, 'js')).filter((f) => f.endsWith('.js') && f !== 'mark.js')) {
  read(`js/${file}`).split('\n').forEach((text, i) => {
    if (/^\s*\/\//.test(text)) return;
    if (/#[0-9a-fA-F]{6}\b/.test(text)) fail(`js/${file}`, i + 1, text, 'a hex colour in a script: use a class');
    if (/style="[^"$]*\d+px/.test(text)) fail(`js/${file}`, i + 1, text, 'a px length in an inline style: use a class');
  });
}

// Every token is used somewhere, or it is not part of the language.
const all = ['css/base.css', 'css/components.css', 'css/wire.css'].map(read).join('\n') + read('../../system/tokens.css').replace(/(--o-[a-z0-9-]+)\s*:/g, '');
const unused = [...defined].filter((name) => !name.startsWith('--o-palette-') && !all.includes(`var(${name})`) && !all.includes(`var(${name},`));
if (unused.length) console.log(`tokens no sheet reads (${unused.length}): ${unused.join(' ')}`);

if (failures) { console.error(`\n${failures} problem${failures === 1 ? '' : 's'}`); process.exit(1); }
console.log('css and scripts: every colour, length, duration and font is a token');
