// Real embedded assets and browser storage; wallpaper files stay in the browser.
const assert=require('node:assert/strict');
const {spawn,spawnSync}=require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync,readFileSync}=require('node:fs');
const path=require('node:path');
const {tmpdir}=require('node:os');
const {chromium}=require('playwright');
const {chooseOption}=require('./select-helpers.cjs');

(async()=>{
 const {freePort,until}=await import('./fixtures.js');
 const root=path.resolve(__dirname,'..'),directory=mkdtempSync(path.join(tmpdir(),'pm-wallpaper-'));
 const executable=process.env.MANAGER_GO_BINARY || path.join(directory,process.platform==='win32'?'manager.exe':'manager');
 if(!process.env.MANAGER_GO_BINARY) {
  const result=spawnSync(process.env.GO_BIN || 'go',['build','-mod=readonly','-trimpath','-o',executable,'.'],{cwd:root,env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit',windowsHide:true});
  assert.equal(result.status,0);
 }
 const base=`http://127.0.0.1:${await freePort()}/app/phicomm-manager/`;
 const child=spawn(executable,['--dev','--listen',new URL(base).host,'--data',directory],{windowsHide:true,stdio:'ignore'});
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
 try {
  await until(async()=>{try{return (await fetch(base)).ok;}catch{return false;}});
  const response=await fetch(base+'images/wallpaper-default.webp');assert.equal(response.status,200);assert.equal(response.headers.get('content-type'),'image/webp');
  assert.ok((await response.arrayBuffer()).byteLength>1000);
  assert.equal((await fetch(base+'images/wallpaper-default.webp',{method:'HEAD'})).status,200);
  const session=await (await fetch(base+'api/session')).json();
  const added=await fetch(base+'api/devices',{method:'POST',headers:{'Content-Type':'application/json',Origin:new URL(base).origin,'X-Manager-CSRF':session.csrfToken},body:JSON.stringify({model:'TC1',method:'mqtt',mac:'aabbcc001601',name:'透明度测试排插',room:'书房'})});
  assert.equal(added.status,201);
  const context=await browser.newContext({viewport:{width:1100,height:720},colorScheme:'light'});
  await context.route('**/api/discovery/automatic?*',r=>r.fulfill({json:{state:'complete',devices:[]}}));
  const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  const mode=()=>page.locator('html').getAttribute('data-wallpaper');
  async function appearance() {
   await page.getByRole('button',{name:'设置',exact:true}).click();
   const settings=page.getByRole('dialog',{name:'设置',exact:true});
   await settings.getByRole('button',{name:'外观',exact:true}).click();return settings;
  }
  await page.goto(base);await page.getByRole('button',{name:'设置',exact:true}).waitFor();assert.equal(await mode(),'default');
  assert.ok(await page.evaluate(()=>getComputedStyle(document.body,'::before').backgroundImage.includes('wallpaper-default.webp')));
  assert.equal(await page.evaluate(async()=>{const image=new Image();image.src='./images/wallpaper-default.webp';await image.decode();return image.naturalWidth;}),1125);
  let settings=await appearance();
  const slider=()=>settings.getByRole('slider',{name:'壁纸透明度',exact:true});
  const cardSlider=()=>settings.getByRole('slider',{name:'卡片透明度',exact:true});
  const opacity=()=>page.evaluate(()=>getComputedStyle(document.body,'::before').opacity);
  const cardAlpha=()=>page.locator('.pm-tile').evaluate(el=>{const ctx=document.createElement('canvas').getContext('2d');ctx.fillStyle=getComputedStyle(el).backgroundColor;ctx.fillRect(0,0,1,1);return ctx.getImageData(0,0,1,1).data[3];});
  assert.equal(await slider().inputValue(),'50');assert.equal(await opacity(),'0.5');
  await slider().press('End');assert.equal(await opacity(),'0');
  await slider().press('Home');assert.equal(await opacity(),'1');
  for(let i=0;i<75;i++)await slider().press('ArrowRight');
  assert.equal(await opacity(),'0.25');
  assert.equal(await page.locator('.pm-app').evaluate(el=>getComputedStyle(el).opacity),'1','only the background may fade');
  assert.equal(await cardSlider().inputValue(),'30');assert.ok(Math.abs(await cardAlpha()-178.5)<=1);
  assert.equal(await page.locator('.pm-top').evaluate(el=>getComputedStyle(el).backgroundColor),'rgba(243, 247, 248, 0.7)');
  await cardSlider().press('End');assert.equal(await cardAlpha(),0);
  await cardSlider().press('Home');assert.equal(await cardAlpha(),255);
  for(let i=0;i<50;i++)await cardSlider().press('ArrowRight');
  assert.equal(await cardAlpha(),128);assert.equal(await opacity(),'0.25','card opacity must not change wallpaper opacity');
  assert.equal(await page.locator('.pm-top').evaluate(el=>getComputedStyle(el).backgroundColor),'rgba(243, 247, 248, 0.5)');
  assert.equal(await page.locator('.pm-tile').evaluate(el=>getComputedStyle(el).opacity),'1');
  assert.equal(await page.locator('.pm-tile-name').evaluate(el=>getComputedStyle(el).opacity),'1','text remains fully opaque');
  const file={name:'自选壁纸.webp',mimeType:'image/webp',buffer:readFileSync(path.join(root,'public/images/wallpaper-default.webp'))};
  let writes=0;page.on('request',r=>{if(['POST','PUT'].includes(r.method()))writes++;});
  await settings.getByLabel('选择壁纸图片',{exact:true}).setInputFiles(file);
  await until(async()=>await mode()==='custom');
  assert.equal(writes,0,'selecting wallpaper must not upload the image to the NAS');
  assert.equal(await settings.getByRole('button',{name:'自定义壁纸',exact:true}).getAttribute('aria-pressed'),'true');
  await settings.getByText('自选壁纸.webp',{exact:true}).waitFor();
  const second=await context.newPage();await second.goto(base);await second.getByRole('button',{name:'设置',exact:true}).waitFor();
  assert.equal(await second.locator('html').getAttribute('data-wallpaper'),'custom');
  assert.equal(await second.evaluate(()=>getComputedStyle(document.body,'::before').opacity),'0.25');
  assert.equal(await second.evaluate(()=>document.documentElement.style.getPropertyValue('--pm-card-opacity')),'0.5');
  await page.reload();await page.getByRole('button',{name:'设置',exact:true}).waitFor();assert.equal(await mode(),'custom');
  settings=await appearance();
  assert.equal(await slider().inputValue(),'75');assert.equal(await opacity(),'0.25');
  assert.equal(await cardSlider().inputValue(),'50');assert.equal(await cardAlpha(),128);
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.getByRole('button',{name:'列表',exact:true}).click();
  const panelAlphas=await page.locator('.pm-workspace>.pm-panel').evaluateAll(els=>els.map(el=>{const ctx=document.createElement('canvas').getContext('2d');ctx.fillStyle=getComputedStyle(el).backgroundColor;ctx.fillRect(0,0,1,1);return ctx.getImageData(0,0,1,1).data[3];}));
  assert.deepEqual(panelAlphas,[128,128],'list panels share the card surface setting');
  await page.getByRole('button',{name:'空间',exact:true}).click();settings=await appearance();
  await settings.getByRole('button',{name:'纯色背景',exact:true}).click();
  assert.equal(await mode(),'plain');assert.equal(await page.evaluate(()=>getComputedStyle(document.body,'::before').backgroundImage),'none');
  assert.equal(await opacity(),'1');assert.equal(await slider().isDisabled(),true);
  assert.equal(await cardSlider().isEnabled(),true);assert.equal(await cardAlpha(),128);
  await until(async()=>await second.locator('html').getAttribute('data-wallpaper')==='plain');
  await settings.getByRole('button',{name:'默认壁纸',exact:true}).click();assert.equal(await mode(),'default');
  assert.equal(await opacity(),'0.25');
  await slider().press('ArrowRight');
  await until(async()=>await second.evaluate(()=>getComputedStyle(document.body,'::before').opacity)==='0.24');
  await cardSlider().press('ArrowRight');
  await until(async()=>await second.evaluate(()=>document.documentElement.style.getPropertyValue('--pm-card-opacity'))==='0.49');
  const input=settings.getByLabel('选择壁纸图片',{exact:true});
  await input.setInputFiles({name:'bad.svg',mimeType:'image/svg+xml',buffer:Buffer.from('<svg/>')});
  await settings.getByRole('alert').filter({hasText:'请选择 JPG'}).waitFor();assert.equal(await mode(),'default');
  await input.setInputFiles({name:'broken.png',mimeType:'image/png',buffer:Buffer.from('not an image')});
  await settings.getByRole('alert').filter({hasText:'无法读取'}).waitFor();assert.equal(await mode(),'default');
  await page.evaluate(()=>{window.wallpaperSetItem=Storage.prototype.setItem;Storage.prototype.setItem=function(key,value){if(key==='pm-wallpaper'||key==='pm-wallpaper-transparency'||key==='pm-card-transparency')throw new DOMException('full','QuotaExceededError');return window.wallpaperSetItem.call(this,key,value);};});
  await settings.getByRole('button',{name:'纯色背景',exact:true}).click();
  await settings.getByRole('alert').filter({hasText:'壁纸未能保存'}).waitFor();assert.equal(await mode(),'default','failed persistence must not show a false applied state');
  await slider().press('ArrowRight');
  await settings.getByRole('alert').filter({hasText:'透明度未能保存'}).waitFor();
  assert.equal(await slider().inputValue(),'76');assert.equal(await opacity(),'0.24');
  await cardSlider().press('ArrowRight');
  await settings.getByRole('alert').filter({hasText:'卡片透明度未能保存'}).waitFor();assert.equal(await cardSlider().inputValue(),'51');
  await page.evaluate(()=>{Storage.prototype.setItem=window.wallpaperSetItem;delete window.wallpaperSetItem;});
  await settings.getByRole('button',{name:'移除自定义图片',exact:true}).click();
  assert.equal(await page.evaluate(()=>JSON.parse(localStorage.getItem('pm-wallpaper')).custom),null);
  await slider().press('Home');for(let i=0;i<50;i++)await slider().press('ArrowRight');
  await cardSlider().press('ArrowLeft');assert.equal(await cardSlider().inputValue(),'50');
  const output=path.join(root,'test-results');mkdirSync(output,{recursive:true});
  for(const theme of ['light','dark']) {
   await chooseOption(settings.getByLabel('主题',{exact:true}),theme);
   for(const width of [320,390,900,1440]) {
    await page.setViewportSize({width,height:width<=390?844:900});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    const box=await settings.evaluate(el=>({overflow:el.scrollWidth>el.clientWidth,bottom:el.getBoundingClientRect().bottom,footer:el.querySelector('.pm-dialog-footer').getBoundingClientRect().bottom,height:innerHeight}));
    assert.equal(box.overflow,false);assert.ok(box.bottom<=box.height+1&&box.footer<=box.height+1);
    const position=await page.evaluate(()=>getComputedStyle(document.body,'::before').backgroundPosition);
    assert.ok(width<=700?position.includes('62%'):!position.includes('62%'),`subject crop at ${width}px: ${position}`);
    await settings.getByRole('button',{name:'默认壁纸',exact:true}).scrollIntoViewIfNeeded();
    if(width===390||width===900)await settings.screenshot({path:path.join(output,`wallpaper-settings-${theme}-${width}.png`)});
   }
  }
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'wallpaper-mobile-subject.png'),fullPage:true});
  assert.deepEqual(errors,[]);await second.close();
  console.log('Wallpaper passed: embedded WebP/HEAD, default and custom/plain modes, independent wallpaper/card transparency with opaque content and shared list panels, browser-only import, reload/cross-tab persistence, invalid image and quota failures, light/dark, centered mobile subject and 320–1440px.');
 } finally {
  await browser.close();
  if(child.exitCode===null){child.kill();await new Promise(resolve=>child.once('exit',resolve));}
  rmSync(directory,{recursive:true,force:true,maxRetries:5,retryDelay:200});
 }
})().catch(error=>{console.error(error);process.exitCode=1;});
