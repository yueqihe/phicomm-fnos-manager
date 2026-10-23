import React, {useEffect, useRef, useState} from 'react';
import {api, json, DeviceIcon} from './ui.jsx';

export function Discovery({choose}) {
  const [data,setData]=useState(null), [adapter,setAdapter]=useState(''), [range,setRange]=useState(''), [error,setError]=useState(''), [starting,setStarting]=useState(false);
  const sequence=useRef(0), timer=useRef(), alive=useRef(false), abort=useRef();
  const accept = snapshot => {
    setData(snapshot);
    if(snapshot.interface) setAdapter(snapshot.interface);
    if(snapshot.range) setRange(snapshot.range);
  };
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
      if(!current?.interfaces.length) {setData(current); return;}
      const input=initial ? {mode:'auto'} : {mode:'auto',interface:adapter,cidr:range.trim()};
      const snapshot=await api('discovery',{...json('POST',input),signal:abort.current.signal});
      if(!alive.current || generation !== sequence.current) return;
      accept(snapshot);
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
  else if(data && !data.interfaces.length) message='未找到可用的局域网接口，可在下方手动添加。';
  else if(scanning) message=`正在扫描设备${data.total ? `：${data.checked} / ${data.total}` : ''}，已发现 ${count} 台。`;
  else if(data?.state === 'cancelled') message=`已停止扫描，发现 ${count} 台设备。`;
  else if(data) message=count ? `发现 ${count} 台设备。` : '未发现设备，请确认设备与 NAS 在同一局域网，或在下方手动添加。';
  return <section className="pm-discovery"><div className="pm-discovery-tools"><button className="pm-btn" type="button" data-scan disabled={starting || scanning || !data?.interfaces.length} onClick={() => scan()}>扫描设备</button>{scanning && <button className="pm-btn" type="button" data-stop-scan disabled={starting} onClick={stop}>停止</button>}</div>
    <details className="pm-discovery-advanced"><summary>高级选项</summary><label>网络接口<select className="pm-input" aria-label="网络接口" value={adapter} disabled={starting || scanning} onChange={e => {setAdapter(e.target.value);setRange(data.interfaces.find(i => i.id === e.target.value)?.range || '');}}>{data?.interfaces.map(i => <option key={i.id} value={i.id}>{i.label}</option>)}</select></label><label>扫描网段<input className="pm-input" aria-label="扫描网段" value={range} disabled={starting || scanning} onChange={e => setRange(e.target.value)} autoComplete="off" autoCapitalize="none" spellCheck={false}/></label><p className="pm-help">默认自动选择局域网。多网卡或需要指定网段时可在此调整，再点击扫描设备。</p></details>
    <p className="pm-help" data-scan-status role="status" aria-live="polite">{message}</p><div data-discovered>{data?.devices.map((d,i) => <button className="pm-discovered" type="button" key={d.id} data-discovered-index={i} disabled={d.added} onClick={() => choose(d)}><span><DeviceIcon device={d}/><strong>{d.model}</strong><small>{d.address} · {d.mac}</small></span><span>{d.added ? '已添加' : '选择'}</span></button>)}{!!data?.protectedAddresses.length && <details><summary>需要密码验证的地址（{data.protectedAddresses.length}）</summary><p className="pm-help">选择地址后填写型号和设备网页密码，验证后接入。</p>{data.protectedAddresses.map((address,i) => <button className="pm-discovered" type="button" key={address} data-protected-index={i} onClick={() => choose({address,protected:true})}><span>{address}</span><span>填写</span></button>)}</details>}</div>
  </section>;
}
