const { chromium } = require('playwright');
const { readFileSync, writeFileSync, mkdirSync } = require('node:fs');
const path = require('node:path');

(async () => {
  const root = path.resolve(__dirname, '../fnos'), source = readFileSync(path.join(root, 'app/icon.svg'), 'utf8');
  const browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE } : {}) });
  try {
    for (const size of [64, 256]) {
      const page = await browser.newPage({ viewport: { width: size, height: size }, deviceScaleFactor: 1 });
      await page.setContent(`<style>html,body{margin:0;background:transparent}svg{display:block;width:100vw;height:100vh}</style>${source}`);
      const png = await page.screenshot({ omitBackground: true });
      mkdirSync(path.join(root, 'app/ui/images'), { recursive: true });
      writeFileSync(path.join(root, size === 64 ? 'ICON.PNG' : 'ICON_256.PNG'), png);
      writeFileSync(path.join(root, `app/ui/images/icon_${size}.png`), png);
      await page.close();
    }
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
