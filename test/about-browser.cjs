/* Exercise the real Go settings UI with deterministic release responses. */
const {chromium}=require('playwright');
const {spawn,spawnSync}=require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync}=require('node:fs');
const {tmpdir}=require('node:os');
const path=require('node:path');
const assert=require('node:assert/strict');
(async()=>{
 const {freePort}=await import('./fixtures.js');
 const root=path.resolve(__dirname,'..'),directory=mkdtempSync(path.join(tmpdir(),'pm-about-browser-'));
 const executable=path.join(directory,process.platform==='win32'?'manager.exe':'manager');
 const build=spawnSync(process.env.GO_BIN || 'go',['build','-mod=readonly','-trimpath','-o',executable,'.'],{cwd:root,env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit',windowsHide:true});
 assert.equal(build.status,0);
 const url=`http://127.0.0.1:${await freePort()}/app/phicomm-manager/`;
 const child=spawn(executable,['--dev','--listen',new URL(url).host,'--data',directory],{windowsHide:true,stdio:['ignore','pipe','pipe']});
 let logs='';child.stdout.on('data',c=>logs+=c);child.stderr.on('data',c=>logs+=c);
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
 try {
  let ready=false;
  for(let i=0;i<80;i++){try {if((await fetch(url)).ok){ready=true;break;}}catch{} await new Promise(r=>setTimeout(r,100));}
  assert.ok(ready,logs);
  const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:'light'}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await context.route('**/api/discovery/automatic?*',route=>route.fulfill({json:{state:'complete',devices:[]}}));
  const project='https://github.com/yueqihe/phicomm-fnos-manager';
  const localVersion=require('../package.json').version;
  let mode='current',requests=[];
  await context.route('**/api/updates',async route=>{
   requests.push(route.request().method());
   await new Promise(r=>setTimeout(r,120));
   if(mode==='network') return route.fulfill({status:502,json:{error:'测试：连接暂不可用'}});
   const newer=mode==='update'||mode==='missing';
   return route.fulfill({json:{status:newer?'update':mode,message:mode==='error'?'暂时无法连接 GitHub，请稍后重试或查看发布页面':newer?'发现新版本':mode==='ahead'?'当前版本高于 GitHub 最新正式版':'当前已是最新正式版',currentVersion:localVersion,latestVersion:newer?'0.2.99':localVersion,checkedAt:'2026-11-05T04:00:00Z',architecture:'ARM64',releaseUrl:project+'/releases/tag/v0.2.99',...(mode==='update'?{downloadUrl:project+'/releases/download/v0.2.99/phicomm-manager-0.2.99-arm.fpk'}:{}),notes:'- 查询性能优化\n- 保留原有数据\n<script>window.aboutInjected=true</script>\n'+('LongReleaseText'.repeat(30))}});
  });
  await page.goto(url);
  await page.getByRole('button',{name:'设置',exact:true}).click();
  const settings=page.getByRole('dialog',{name:'设置',exact:true});
  await settings.getByRole('button',{name:'关于',exact:true}).click();
  await settings.getByText('当前已是最新正式版',{exact:true}).waitFor();
  assert.deepEqual(requests,['GET']);
  assert.ok((await settings.locator('.pm-about-info').textContent()).includes(localVersion));
  assert.equal(await settings.getByRole('button',{name:'保存配置',exact:true}).count(),0);
  assert.equal(await settings.getByRole('link',{name:'情深深雨蒙蒙',exact:true}).getAttribute('href'),'https://github.com/yueqihe');
  mode='update';await settings.getByRole('button',{name:'检查更新',exact:true}).click();
  await settings.getByText('发现新版本',{exact:true}).waitFor();
  assert.deepEqual(requests,['GET','POST']);
  const download=settings.getByRole('link',{name:'下载 ARM64 安装包',exact:true});
  assert.equal(await download.getAttribute('href'),project+'/releases/download/v0.2.99/phicomm-manager-0.2.99-arm.fpk');
  assert.equal(await download.getAttribute('rel'),'noopener noreferrer');
  assert.equal(await page.evaluate(()=>window.aboutInjected),undefined,'release notes must be rendered as text');
  assert.equal(await settings.locator('.pm-release-notes script').count(),0);
  const output=path.join(root,'test-results');mkdirSync(output,{recursive:true});
  for(const theme of ['light','dark']) {
   await page.evaluate(value=>{document.documentElement.dataset.theme=value;},theme);
   for(const width of [320,390,768,1440]) {
    await page.setViewportSize({width,height:900});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    assert.equal(await settings.locator('.pm-dialog-body').evaluate(el=>el.scrollWidth>el.clientWidth),false,`about overflow ${theme}/${width}`);
    if(width===390) assert.ok((await download.boundingBox()).height>=44,'mobile download target too small');
    if(width===390||width===1440) await page.screenshot({path:path.join(output,`about-${theme}-${width}.png`),fullPage:true});
   }
  }
  for(const state of ['missing','ahead','error','network','current']) {
   mode=state;await settings.getByRole('button',{name:'检查更新',exact:true}).click();
   await settings.getByRole('button',{name:'检查更新',exact:true}).waitFor();
   assert.equal(await settings.getByRole('link',{name:/下载 .* 安装包/}).count(),0);
   if(state==='missing') await settings.getByText('此版本暂无匹配当前架构的安装包，可在发布页面查看。',{exact:true}).waitFor();
   if(state==='error'||state==='network') assert.ok((await settings.locator('.pm-update-status').getAttribute('class')).includes('is-error'));
  }
  await page.keyboard.press('Escape');await settings.waitFor({state:'hidden'});
  assert.deepEqual(errors,[]);
  console.log('About browser passed: version, author, automatic/manual check, update/current/ahead/error states, safe notes, architecture link, 320–1440px and light/dark.');
 } finally {
  await browser.close();child.kill();await new Promise(resolve=>child.exitCode!==null?resolve():child.once('exit',resolve));
  rmSync(directory,{recursive:true,force:true});
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
