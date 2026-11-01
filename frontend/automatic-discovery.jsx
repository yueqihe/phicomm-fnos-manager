import React, {useCallback, useEffect, useRef, useState} from 'react';
import {api, json, DeviceIcon} from './ui.jsx';

const scanInterval=60000;

export function useAutomaticDiscovery(enabled, devices) {
  const [found,setFound]=useState([]), [dismissed,setDismissed]=useState(new Set());
  const client=useRef('');
  if(!client.current) client.current=Array.from(crypto.getRandomValues(new Uint8Array(16)), n => n.toString(16).padStart(2,'0')).join('');
  useEffect(() => {
    if(!enabled) return;
    let active=true, running=false, lastStart=0, interval, pollTimer, controller;
    const endpoint=`discovery/automatic?client=${client.current}`;
    const accept=snapshot => {
      const candidates=snapshot.devices.filter(d => !d.added && !d.ignored);
      setFound(old => snapshot.state === 'scanning' ? [...new Map([...old,...candidates].map(d => [d.id,d])).values()] : candidates);
      if(snapshot.state === 'scanning') pollTimer=setTimeout(() => request(false),3000);
    };
    async function request(start) {
      if(!active || running) return;
      running=true;
      controller=new AbortController();
      // An unresponsive request must not leave this page permanently scanning.
      const timeout=setTimeout(() => controller.abort(),12000);
      try {
        const snapshot=await api(endpoint,{...(start ? json('POST',{}) : {}),signal:controller.signal});
        if(active) accept(snapshot);
      } catch(e) {
        if(active && e.status === 401) stop();
        // Discovery failures are quiet; device collection and manual setup stay usable.
      } finally {clearTimeout(timeout);running=false;if(active && !lastStart) scan();}
    }
    function scan() {
      if(!active || running || Date.now()-lastStart < scanInterval) return;
      clearTimeout(pollTimer);
      lastStart=Date.now();
      request(true);
    }
    function stop() {
      active=false;clearInterval(interval);clearTimeout(pollTimer);controller?.abort();
      api(endpoint,{method:'DELETE',keepalive:true},false).catch(() => {});
    }
    function resume() {
      if(!active) {active=true;lastStart=0;interval=setInterval(scan,scanInterval);}
      scan();
    }
    interval=setInterval(scan,scanInterval);
    scan();
    window.addEventListener('pagehide',stop);
    window.addEventListener('pageshow',resume);
    window.addEventListener('online',scan);
    return () => {stop();window.removeEventListener('pagehide',stop);window.removeEventListener('pageshow',resume);window.removeEventListener('online',scan);};
  },[enabled]);
  const dismiss=useCallback(id => setDismissed(old => new Set([...old,id])),[]);
  const registered=new Set(devices.map(d => d.id));
  return {candidates:found.filter(d => !registered.has(d.id) && !dismissed.has(d.id)),dismiss};
}

export function NewDevicesNotice({devices,add,dismiss}) {
  if(!devices.length) return null;
  return <section className="pm-new-devices" aria-label="发现新设备">
    <p role="status" aria-live="polite">发现 {devices.length} 台新设备，是否添加？</p>
    <ul>{devices.map(d => <li key={d.id}>
      <div className="pm-new-device-info"><DeviceIcon device={d}/><div><strong>{d.model === 'DAIKIN' ? 'DAIKIN 大金检测仪' : d.model}</strong><span>{d.address} · {d.mac}</span></div></div>
      <div className="pm-new-device-actions"><button className="pm-btn pm-btn-primary" aria-label={`添加 ${d.model} ${d.mac}`} onClick={() => add(d)}>添加</button><button className="pm-btn" aria-label={`暂不添加 ${d.model} ${d.mac}`} onClick={() => dismiss(d.id)}>暂不添加</button></div>
    </li>)}</ul>
  </section>;
}
