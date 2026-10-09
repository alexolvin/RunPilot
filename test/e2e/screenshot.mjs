// HELD screenshot: headless Chrome → PNG of the RunPilot web UI.
// Usage: node test/e2e/screenshot.mjs <stand_url> <token> <output.png>
//
// stand_url — http://127.0.0.1:18789 (web port)
// token     — stand token (≥16 chars)
// output    — path to the output PNG

import { chromium } from '/home/user/.nvm/versions/node/v24.18.0/lib/node_modules/@playwright/mcp/node_modules/playwright-core/index.mjs';

const [,, standUrl, token, output] = process.argv;
if (!standUrl || !token || !output) {
  console.error('Usage: node screenshot.mjs <stand_url> <token> <output.png>');
  process.exit(1);
}

const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.HOME + '/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome',
  args: ['--no-sandbox'],
});
const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 } });

// Login: POST /web/login {token} → cookie runpilot_session
const resp = await ctx.request.post(standUrl + '/web/login', {
  data: { token },
});
if (resp.status() !== 200) {
  console.error('Login failed:', resp.status());
  await browser.close();
  process.exit(1);
}

// Navigate to the queue page (where HELD sessions are shown)
const page = await ctx.newPage();
await page.goto(standUrl + '/queue', { waitUntil: 'networkidle' });
// Wait for the queue to render (SPA + SSE)
await page.waitForTimeout(2000);

await page.screenshot({ path: output, fullPage: false });
console.log('Screenshot saved to', output);
await browser.close();
