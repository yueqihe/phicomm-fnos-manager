import React, {useEffect, useRef, useState} from 'react';
import {api, json, when} from './ui.jsx';

const ExternalLink = ({href,children,className=''}) => <a className={className} href={href} target="_blank" rel="noopener noreferrer">{children}</a>;

export function AboutPanel() {
  const [about,setAbout]=useState(null), [update,setUpdate]=useState(null), [error,setError]=useState(''), [checking,setChecking]=useState(true);
  const abort=useRef(), lock=useRef(false);
  async function check(force=false,signal=abort.current?.signal) {
    if(lock.current || signal?.aborted) return;
    lock.current=true;setChecking(true);setError('');
    try {
      if(!about) {
        const info=await api('about',{signal});
        if(signal?.aborted) return;
        setAbout(info);
      }
      const result=await api('updates',{...(force ? json('POST',{}) : {}),signal});
      if(!signal?.aborted) setUpdate(result);
    } catch(e) {if(!signal?.aborted) setError(e.message);}
    finally {lock.current=false;if(!signal?.aborted) setChecking(false);}
  }
  useEffect(() => {
    const controller=new AbortController();abort.current=controller;
    check(false,controller.signal);
    return () => controller.abort();
  },[]);
  const failed=!checking && (!!error || update?.status === 'error'), available=!checking && !failed && update?.status === 'update';
  const message=error || (checking ? '正在检查 GitHub 最新版本…' : update?.message);
  return <section className="pm-about" aria-label="关于 IoT 设备管理">
    <h3>IoT 设备管理</h3>
    <p className="pm-about-description">通过飞牛查看和控制你的 IoT 设备</p>
    <dl className="pm-about-info">
      <div><dt>当前版本</dt><dd>{about ? <>{about.version}<span className="pm-about-arch">{about.architecture}</span></> : '正在读取…'}</dd></div>
      <div><dt>作者</dt><dd><ExternalLink href="https://github.com/yueqihe">情深深雨蒙蒙</ExternalLink></dd></div>
    </dl>
    <div className="pm-about-links"><ExternalLink href="https://github.com/yueqihe/phicomm-fnos-manager">GitHub 项目</ExternalLink><ExternalLink href="https://github.com/phicomm-io/">上游社区</ExternalLink></div>
    <section className="pm-update" aria-label="版本更新" aria-busy={checking}>
      <div className="pm-update-heading"><h4>版本更新</h4><button className="pm-btn" type="button" disabled={checking} onClick={() => check(true)}>{checking ? '正在检查…' : '检查更新'}</button></div>
      <div className={`pm-update-status${failed ? ' is-error' : available ? ' is-available' : ''}`} role="status" aria-live="polite"><p>{message}</p>{available && <strong>v{update.latestVersion}</strong>}</div>
      {update?.checkedAt && <p className="pm-about-meta">上次检查 {when(update.checkedAt)}</p>}
      <div className="pm-update-actions">
        {available && update.downloadUrl && <ExternalLink className="pm-btn pm-btn-primary" href={update.downloadUrl}>下载 {update.architecture} 安装包</ExternalLink>}
        <ExternalLink href={update?.releaseUrl || 'https://github.com/yueqihe/phicomm-fnos-manager/releases'}>查看 GitHub 发布页面</ExternalLink>
      </div>
      {available && <p className="pm-help">{update.downloadUrl ? '下载后在飞牛应用中心手动升级，保留应用数据。' : '此版本暂无匹配当前架构的安装包，可在发布页面查看。'}</p>}
      <p className="pm-about-meta">打开关于时自动检查，结果缓存 15 分钟。</p>
      {available && update.notes && <details className="pm-release-notes" open><summary>更新说明</summary><div>{update.notes}</div></details>}
      {available && !update.notes && <p className="pm-about-meta">更新说明请查看 GitHub 发布页面。</p>}
    </section>
  </section>;
}
