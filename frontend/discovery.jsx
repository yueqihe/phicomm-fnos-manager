import React, {useEffect, useRef, useState} from 'react';
import {api, json, Select, DeviceIcon} from './ui.jsx';

export function Discovery({choose}) {
  const [data,setData]=useState(null), [adapter,setAdapter]=useState(''), [range,setRange]=useState(''), [error,setError]=useState(''), [starting,setStarting]=useState(false);
  const sequence=useRef(0), timer=useRef(), alive=useRef(false), abort=useRef();
  async function poll(generation) {
    try {
      const snapshot=await api('discovery',{signal:abort.current.signal});
      if(!alive.current || generation !== sequence.current) return;
      // Polling must preserve drafts in the advanced fields.
      setData(snapshot);
      if(snapshot.state === 'scanning') timer.current=setTimeout(() => poll(generation),700);
    } catch(e) {if(alive.current && generation === sequence.current && e.name !== 'AbortError') setError(e.message);}
  }
  async function scan(initial=false) {
    const generation=++sequence.current;
    clearTimeout(timer.current); abort.current?.abort(); abort.current=new AbortController();
    setStarting(true); setError('');
    try {
      const current=initial ? await api('discovery',{signal:abort.current.signal}) : data;
      if(!alive.current || generation !== sequence.current) return;
      if(initial) setData(current);
      if(initial && !current?.interfaces.length) {if(current?.state === 'scanning') timer.current=setTimeout(() => poll(generation),700);return;}
      const input=initial ? {mode:'auto'} : range.trim() ? {mode:'lan',cidr:range.trim()} : {mode:'auto',...(adapter ? {interface:adapter} : {})};
      const snapshot=await api('discovery',{...json('POST',input),signal:abort.current.signal});
      if(!alive.current || generation !== sequence.current) return;
      setData(snapshot);
      if(snapshot.state === 'scanning') timer.current=setTimeout(() => poll(generation),700);
    } catch(e) {if(alive.current && generation === sequence.current && e.name !== 'AbortError') setError(e.message);}
    finally {if(alive.current && generation === sequence.current) setStarting(false);}
  }
  async function stop() {
    ++sequence.current; clearTimeout(timer.current); abort.current?.abort(); setStarting(true);
    try {const snapshot=await api('discovery',{method:'DELETE'}); if(alive.current) {setData(snapshot); setError('');}}
    catch(e) {if(alive.current) setError(e.message);}
    finally {if(alive.current) setStarting(false);}
  }
  useEffect(() => {
    alive.current=true; scan(true);
    return () => {alive.current=false; ++sequence.current; clearTimeout(timer.current); abort.current?.abort(); api('discovery',{method:'DELETE'}).catch(() => {});};
  },[]);
  const scanning=data?.state === 'scanning', count=data?.devices.length || 0;
  let message='正在扫描设备…';
  if(error) message=error;
  else if(scanning) message=`正在扫描设备${data.total ? `：${data.checked} / ${data.total}` : ''}，已发现 ${count} 台。`;
  else if(data?.state === 'cancelled') message=`已停止扫描，发现 ${count} 台设备。`;
  else if(data && !data.interfaces.length && !count && !data.range) message='未读取到局域网接口，可手动指定网段扫描，或在下方手动添加设备。';
  else if(data) message=count ? `发现 ${count} 台设备。` : '未发现设备，可手动指定 NAS 能访问的其他内网网段，或在下方手动添加。';
  return <section className="pm-discovery"><div className="pm-discovery-tools"><button className="pm-btn" type="button" data-scan disabled={starting || scanning && !range.trim() || !(data?.interfaces.length || range.trim())} onClick={() => scan()}>扫描设备</button>{scanning && !data?.background && <button className="pm-btn" type="button" data-stop-scan disabled={starting} onClick={stop}>停止</button>}</div>
    <details className="pm-discovery-advanced"><summary>手动指定网段</summary><label>网络接口<Select aria-label="网络接口" value={adapter} disabled={starting || scanning} onChange={e => {setAdapter(e.target.value);setRange(data.interfaces.find(i => i.id === e.target.value)?.range || '');setError('');}}><option value="">自动选择</option>{data?.interfaces.map(i => <option key={i.id} value={i.id}>{i.label}</option>)}</Select></label><label>扫描网段（仅本次）<input className="pm-input" aria-label="扫描网段" value={range} disabled={starting} onChange={e => {setRange(e.target.value);setAdapter('');setError('');}} placeholder="例如 192.168.2.0/24 或 192.168.2.20" autoComplete="off" autoCapitalize="none" spellCheck={false}/></label><p className="pm-help">支持 NAS 能访问的其他内网网段；填写后按系统路由扫描，不受当前网卡网段限制。每次最多 1024 个地址（/22 至 /32），留空恢复自动扫描。仅用于本次添加，关闭后不保存。</p></details>
    <p className="pm-help" data-scan-status role="status" aria-live="polite">{message}</p><div data-discovered>{data?.devices.map((d,i) => <button className="pm-discovered" type="button" key={d.id} data-discovered-index={i} disabled={d.added && !d.canUpdate} onClick={() => choose(d)}><span><DeviceIcon device={d}/><strong>{d.model}</strong><small>{d.address} · {d.mac}</small></span><span>{d.recovered ? '已恢复连接' : d.canUpdate ? '更新地址' : d.added ? '已添加' : '选择'}</span></button>)}{!!data?.protectedAddresses.length && <details><summary>需要密码验证的地址（{data.protectedAddresses.length}）</summary><p className="pm-help">选择地址后填写型号和设备网页密码，验证后接入。</p>{data.protectedAddresses.map((address,i) => <button className="pm-discovered" type="button" key={address} data-protected-index={i} onClick={() => choose({address,protected:true})}><span>{address}</span><span>填写</span></button>)}</details>}</div>
  </section>;
}
