// Screenshots of the round-5 wireframe, at 1600 and 1000 wide, into shots/.
//   PLAYWRIGHT_CORE=<a node_modules directory that holds playwright-core> node shoot.mjs [name ...]
// CHROMIUM names the browser binary when it is not the system's.
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
if (!process.env.PLAYWRIGHT_CORE) { console.error('shoot: set PLAYWRIGHT_CORE to a node_modules directory that holds playwright-core'); process.exit(1); }
const require = createRequire(path.join(path.resolve(process.env.PLAYWRIGHT_CORE), '/'));
const { chromium } = require('playwright-core');

// name: the hash that opens the state. hover names an element the pointer rests on before the shot.
const SHOTS = {
  'conversation-working': { hash: 'go=sess:lamp' },
  'conversation-question': { hash: 'go=sess:beacon' },
  'inbox-collapsed': { hash: 'go=sess:lamp' },
  'inbox-flyout-hover': { hash: 'go=sess:lamp', hover: '.b-head [data-inbox]' },
  'inbox-flyout-keys': { hash: 'go=sess:beacon&inbox=fly' },
  'inbox-pinned': { hash: 'go=sess:lamp&inbox=pin' },
  'inbox-browse-project': { hash: 'go=sess:lamp&inbox=browse', keys: ['p'] },
  'ask': { hash: 'go=sess:lamp&ask=Why is the roster page slow for weeks with more than forty shifts?' },
  'contract-card': { hash: 'go=sess:tideplan&cs=fails' },
  'contract-card-decision': { hash: 'go=sess:tideplan&settle=1' },
  'contract-accepted-epic-card': { hash: 'go=sess:tideplan&m=tidealerts.1' },
  'epic-card': { hash: 'go=sess:rotaplan&at=card-epic' },
  'epic-card-question': { hash: 'go=sess:rotaplan' },
  'epic-page-link-back': { hash: 'go=epic:rota' },
  'epic-page-talk-dock': { hash: 'go=epic:rota&talk=rotaplan' },
  'worker-session': { hash: 'go=session:rota:export-cli:1' },
  'contract-page': { hash: 'go=contract:tidealerts' },
  'split': { hash: 'go=sess:lamp', keys: ['|'], widths: [1600] },
  'keys': { hash: 'go=sess:lamp&keys=1', widths: [1600], height: 1500 },
};

const want = process.argv.slice(2);
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || '/run/current-system/sw/bin/chromium', headless: true });
for (const [name, shot] of Object.entries(SHOTS)) {
  if (want.length && !want.includes(name)) continue;
  for (const width of shot.widths || [1600, 1000]) {
    const ctx = await browser.newContext({ viewport: { width, height: shot.height || 1000 }, timezoneId: 'UTC' });
    const page = await ctx.newPage();
    await page.goto(`file://${here}/index.html#${shot.hash}`);
    await page.waitForTimeout(700);
    for (const key of shot.keys || []) { await page.keyboard.press(key); await page.waitForTimeout(400); }
    if (shot.hover) { await page.hover(shot.hover); await page.waitForTimeout(700); }
    await page.waitForTimeout(shot.wait || 3400);   // past the one-second dwell and the note that it wrote seen
    await page.screenshot({ path: path.join(here, 'shots', `${name}-${width}.png`) });
    await ctx.close();
  }
}
await browser.close();
