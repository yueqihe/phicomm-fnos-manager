import React, {useEffect, useId, useRef, useState} from 'react';
import {Icon} from './icons.jsx';

const key='pm-wallpaper', maxStoredLength=2_000_000;
const transparencyKey='pm-wallpaper-transparency';
const cardTransparencyKey='pm-card-transparency';
const defaultCardTransparency=30;
const defaultImage='./images/wallpaper-default.webp';
const empty={mode:'default',custom:null,transparency:50,cardTransparency:defaultCardTransparency};
const imageData=/^data:image\/(?:webp|png|jpeg);base64,[A-Za-z0-9+/]+=*$/;
const transparencyValue=(value,fallback=50) => Number.isFinite(value) ? Math.max(0,Math.min(100,Math.round(value))) : fallback;

function readTransparency() {
  try {const saved=localStorage.getItem(transparencyKey);return saved === null ? 50 : transparencyValue(Number(saved));}
  catch {return 50;}
}

function readCardTransparency() {
  try {const saved=localStorage.getItem(cardTransparencyKey);return saved === null ? defaultCardTransparency : transparencyValue(Number(saved),defaultCardTransparency);}
  catch {return defaultCardTransparency;}
}

function read() {
  const transparency=readTransparency(),cardTransparency=readCardTransparency();
  try {
    const saved=JSON.parse(localStorage.getItem(key));
    if(!saved || !['default','custom','plain'].includes(saved.mode)) return {...empty,transparency,cardTransparency};
    const custom=saved.custom && typeof saved.custom.image === 'string' && saved.custom.image.length<=maxStoredLength && imageData.test(saved.custom.image) ? {image:saved.custom.image,name:typeof saved.custom.name === 'string' ? saved.custom.name.slice(0,160) : '自定义图片'} : null;
    return {mode:saved.mode === 'custom' && !custom ? 'default' : saved.mode,custom,transparency,cardTransparency};
  } catch {return {...empty,transparency,cardTransparency};}
}

function applyTransparency(transparency) {
  document.documentElement.style.setProperty('--pm-wallpaper-opacity',String((100-transparency)/100));
}

function applyCardTransparency(transparency) {
  document.documentElement.dataset.cardTransparency=String(transparency);
  document.documentElement.style.setProperty('--pm-card-opacity',String((100-transparency)/100));
}

function apply(wallpaper) {
  const root=document.documentElement;
  root.dataset.wallpaper=wallpaper.mode;
  applyTransparency(wallpaper.transparency);
  applyCardTransparency(wallpaper.cardTransparency);
  if(wallpaper.mode === 'custom' && wallpaper.custom) root.style.setProperty('--pm-wallpaper-image',`url("${wallpaper.custom.image}")`);
  else root.style.removeProperty('--pm-wallpaper-image');
}
apply(read());

async function prepareImage(file) {
  if(!['image/png','image/jpeg','image/webp','image/avif'].includes(file.type)) throw new Error('请选择 JPG、PNG、WebP 或 AVIF 图片。');
  if(file.size>20*1024*1024) throw new Error('图片过大，请选择 20 MB 以内的图片。');
  let source;
  try {
    try {source=await createImageBitmap(file);} catch {throw new Error('无法读取这张图片，请换一张重试。');}
    if(!source.width || !source.height) throw new Error('图片尺寸无效，请换一张重试。');
    const ratio=Math.min(1,2560/Math.max(source.width,source.height));
    const canvas=document.createElement('canvas');
    canvas.width=Math.max(1,Math.round(source.width*ratio));canvas.height=Math.max(1,Math.round(source.height*ratio));
    canvas.getContext('2d').drawImage(source,0,0,canvas.width,canvas.height);
    for(const quality of [.88,.78,.68,.56]) {
      const image=canvas.toDataURL('image/webp',quality);
      if(image.length<=maxStoredLength && imageData.test(image)) return {image,name:file.name.slice(0,160)};
    }
    throw new Error('图片内容过大，请选择一张较小的图片。');
  } finally {source?.close();}
}

export function useWallpaper() {
  const [wallpaper,setWallpaper]=useState(read);
  useEffect(() => {
    const storage=e => {
      if(e.key === transparencyKey) {
        const transparency=readTransparency();applyTransparency(transparency);setWallpaper(old => ({...old,transparency}));
      } else if(e.key === cardTransparencyKey) {
        const cardTransparency=readCardTransparency();applyCardTransparency(cardTransparency);setWallpaper(old => ({...old,cardTransparency}));
      } else if(e.key === key || e.key === null) {const next=read();apply(next);setWallpaper(next);}
    };
    window.addEventListener('storage',storage);
    return () => window.removeEventListener('storage',storage);
  },[]);
  function save(next) {
    try {localStorage.setItem(key,JSON.stringify({mode:next.mode,custom:next.custom}));}
    catch {throw new Error('壁纸未能保存，浏览器存储空间可能不足。请清理空间后重试。');}
    const current={...next,transparency:readTransparency(),cardTransparency:readCardTransparency()};
    apply(current);setWallpaper(current);
  }
  function setTransparency(value) {
    const transparency=transparencyValue(value);
    // Keep slider writes small instead of repeatedly rewriting the image data.
    try {localStorage.setItem(transparencyKey,String(transparency));}
    catch {throw new Error('透明度未能保存，请清理浏览器存储空间后重试。');}
    applyTransparency(transparency);setWallpaper(old => ({...old,transparency}));
  }
  function setCardTransparency(value) {
    const cardTransparency=transparencyValue(value,defaultCardTransparency);
    try {localStorage.setItem(cardTransparencyKey,String(cardTransparency));}
    catch {throw new Error('卡片透明度未能保存，请清理浏览器存储空间后重试。');}
    applyCardTransparency(cardTransparency);setWallpaper(old => ({...old,cardTransparency}));
  }
  return {...wallpaper,setTransparency,setCardTransparency,choose:mode => save({...wallpaper,mode}),upload:async file => save({...wallpaper,mode:'custom',custom:await prepareImage(file)}),remove:() => save({...wallpaper,mode:wallpaper.mode === 'custom' ? 'default' : wallpaper.mode,custom:null})};
}

export function CardAppearanceField({appearance}) {
  const id=useId(),[error,setError]=useState('');
  return <section className="pm-card-appearance" aria-label="卡片外观">
    <label className="pm-range-label" htmlFor={id}><span>卡片透明度</span><output htmlFor={id}>{appearance.cardTransparency}%</output></label>
    <input id={id} className="pm-range" type="range" min="0" max="100" step="1" value={appearance.cardTransparency} aria-valuetext={`${appearance.cardTransparency}%`} onChange={e => {setError('');try {appearance.setCardTransparency(Number(e.target.value));} catch(error) {setError(error.message);}}}/>
    <div className="pm-range-ends"><span>不透明</span><span>全透明</span></div>
    <p className="pm-help">同时应用于设备卡片、列表面板和顶部工具栏。</p>
    {error && <p className="pm-error" role="alert">{error}</p>}
  </section>;
}

export function WallpaperFields({wallpaper}) {
  const input=useRef(),opacityId=useId(),[busy,setBusy]=useState(false),[error,setError]=useState('');
  function choose(mode) {
    setError('');
    if(mode === 'custom' && !wallpaper.custom) {input.current.click();return;}
    try {wallpaper.choose(mode);} catch(e) {setError(e.message);}
  }
  async function upload(e) {
    const file=e.target.files?.[0];e.target.value='';
    if(!file || busy) return;
    setBusy(true);setError('');
    try {await wallpaper.upload(file);} catch(e) {setError(e.message);}
    finally {setBusy(false);}
  }
  return <section className="pm-wallpaper-settings" aria-label="壁纸设置">
    <h3>壁纸</h3>
    <div className="pm-wallpaper-choices" role="group" aria-label="选择壁纸">{[['default','默认壁纸'],['custom','自定义壁纸'],['plain','纯色背景']].map(([mode,label]) => <button key={mode} type="button" className="pm-wallpaper-choice" aria-pressed={wallpaper.mode === mode} disabled={busy} onClick={() => choose(mode)}>
      <span className={`pm-wallpaper-thumb pm-wallpaper-thumb-${mode}`}>{mode === 'default' ? <img src={defaultImage} alt=""/> : mode === 'custom' ? wallpaper.custom ? <img src={wallpaper.custom.image} alt=""/> : <Icon name="plus"/> : null}{wallpaper.mode === mode && <span className="pm-wallpaper-check"><Icon name="check"/></span>}</span>
      <span>{label}</span>
    </button>)}</div>
    <div className="pm-wallpaper-opacity">
      <label className="pm-range-label" htmlFor={opacityId}><span>壁纸透明度</span><output htmlFor={opacityId}>{wallpaper.transparency}%</output></label>
      <input id={opacityId} className="pm-range" type="range" min="0" max="100" step="1" value={wallpaper.transparency} aria-valuetext={`${wallpaper.transparency}%`} disabled={busy || wallpaper.mode === 'plain'} onChange={e => {setError('');try {wallpaper.setTransparency(Number(e.target.value));} catch(error) {setError(error.message);}}}/>
      <div className="pm-range-ends"><span>不透明</span><span>全透明</span></div>
    </div>
    <input ref={input} type="file" accept="image/png,image/jpeg,image/webp,image/avif" className="pm-sr-only" tabIndex={-1} aria-label="选择壁纸图片" onChange={upload}/>
    <div className="pm-wallpaper-actions"><button type="button" className="pm-btn" disabled={busy} onClick={() => input.current.click()}>{busy ? '正在设置…' : wallpaper.custom ? '更换图片' : '选择图片'}</button>{wallpaper.custom && <button type="button" className="pm-text-action" disabled={busy} onClick={() => {setError('');try {wallpaper.remove();} catch(e) {setError(e.message);}}}>移除自定义图片</button>}</div>
    {wallpaper.custom && <p className="pm-wallpaper-filename" title={wallpaper.custom.name}>{wallpaper.custom.name}</p>}
    <p className="pm-help">支持 JPG、PNG、WebP 和 AVIF。壁纸即时生效并保存在当前浏览器。</p>
    {error && <p className="pm-error" role="alert">{error}</p>}
  </section>;
}
