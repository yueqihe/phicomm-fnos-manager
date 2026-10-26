import React, {useEffect, useId, useRef, useState} from 'react';
import {api, Field, Select, metric} from './ui.jsx';
import {ReadingValue} from './readings.jsx';

const ranges=[['24h','最近 24 小时'],['7d','最近 7 天'],['30d','最近 30 天']];
const stamp=(at, long=false) => new Date(at).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',month:'2-digit',day:'2-digit',...(long ? {hour:'2-digit',minute:'2-digit',hour12:false} : {})});
const clock=at => new Date(at).toLocaleTimeString('zh-CN',{timeZone:'Asia/Shanghai',hour:'2-digit',minute:'2-digit',hour12:false});
const valid=n => typeof n === 'number' && Number.isFinite(n);

export function historyDomain(numbers) {
  const min=Math.min(...numbers), max=Math.max(...numbers);
  const padding=max === min ? Math.max(Math.abs(max)*.04,1) : (max-min)*.1;
  return {low:min < 0 ? min-padding : Math.max(0,min-padding),high:max+padding};
}

// Keep every measured point and stay within each pair's value range. Mixing a
// little monotone curvature into the straight segments softens turns lightly.
function curvePath(points) {
  const slopes=points.slice(1).map(([x,y],i) => (y-points[i][1])/(x-points[i][0]));
  const tangent=i => {
    if(i === 0) return slopes[0];
    if(i === points.length-1) return slopes.at(-1);
    const left=slopes[i-1], right=slopes[i];
    return left*right <= 0 ? 0 : Math.sign(left)*Math.min(Math.abs((left+right)/2),3*Math.min(Math.abs(left),Math.abs(right)));
  };
  return points.map(([x,y],i) => {
    if(!i) return `M${x},${y}`;
    const [px,py]=points[i-1], dx=(x-px)/3, low=Math.min(py,y), high=Math.max(py,y);
    const clamp=n => Math.max(low,Math.min(high,n)), blend=(line,smooth) => line*.75+clamp(smooth)*.25;
    return `C${px+dx},${blend(py+(y-py)/3,py+tangent(i-1)*dx)} ${x-dx},${blend(y-(y-py)/3,y-tangent(i)*dx)} ${x},${y}`;
  }).join(' ');
}

export function HistoryCurve({segments,x,y,baseline}) {
  const gradient=`pm-area-${useId().replace(/[^a-zA-Z0-9_-]/g,'')}`;
  const paths=segments.map(segment => {
    const points=segment.map(([index,n]) => [x(index),y(n)]);
    return {points,line:curvePath(points)};
  });
  return <><defs><linearGradient id={gradient} x1="0" x2="0" y1="0" y2={baseline} gradientUnits="userSpaceOnUse"><stop offset="0%" className="pm-trend-fill-top"/><stop offset="100%" className="pm-trend-fill-bottom"/></linearGradient></defs>
    {paths.map(({points,line},i) => points.length > 1 && <path key={i} d={`${line} L${points.at(-1)[0]},${baseline} L${points[0][0]},${baseline} Z`} fill={`url(#${gradient})`} className="pm-trend-area" aria-hidden="true"/>)}
    {paths.map(({points,line},i) => points.length === 1 ? <circle key={i} cx={points[0][0]} cy={points[0][1]} r="2.5" className="pm-trend-dot"/> : <path key={i} d={line} className="pm-trend-line"/>)}
  </>;
}

function Trend({data,selected}) {
  const [hover,setHover]=useState(null), description=useId();
  useEffect(() => setHover(null),[data,selected.id]);
  const values=data.points.map(p => p.values[selected.id]), numbers=values.filter(valid);
  if(!numbers.length) return <div className="pm-trend-empty">{selected.id === 'cost' ? '暂无计费数据，请先设置电价并等待新的用电读数。' : selected.total ? '需要至少两次有效读数才能统计区间用量。' : '所选时段暂无有效读数，设备上线后会自动记录。'}</div>;
  const domain=historyDomain(numbers), low=selected.total ? Math.min(0,domain.low) : domain.low, top=domain.high;
  const x=i => 8+i/Math.max(1,values.length-1)*704, y=n => 200-(n-low)/(top-low)*184;
  const paths=[];let segment=[];
  values.forEach((n,i) => {if(valid(n)) segment.push([i,n]);else if(segment.length) {paths.push(segment);segment=[];}});
  if(segment.length) paths.push(segment);
  const point=hover == null ? null : data.points[hover], number=point?.values[selected.id];
  const choose=e => {const box=e.currentTarget.getBoundingClientRect();setHover(Math.max(0,Math.min(values.length-1,Math.round(((e.clientX-box.left)/box.width*720-8)/704*(values.length-1)))));};
  return <div className="pm-trend" data-telemetry-chart>
    <div className="pm-trend-info" id={description} aria-live="polite">{point ? <><time>{stamp(point.time,true)}</time><strong><ReadingValue value={number} metricId={selected.id} digits={selected.digits} unit={selected.unit}/></strong></> : <><span>{selected.total ? '每个区间的合计' : '每个区间的平均值'}</span><span>轻点或移动查看 · 方向键选择</span></>}</div>
    <div className="pm-trend-plot"><div className="pm-trend-y"><span>{metric(top,selected.digits)}</span><span>{metric((top+low)/2,selected.digits)}</span><span>{metric(low,selected.digits)}</span></div><div className="pm-trend-canvas" tabIndex={0} role="group" aria-label={`${selected.label}趋势图`} aria-describedby={description} onPointerMove={choose} onPointerDown={choose} onPointerLeave={() => setHover(null)} onKeyDown={e => {if(['ArrowLeft','ArrowRight','Home','End'].includes(e.key)) {e.preventDefault();setHover(i => e.key === 'Home' ? 0 : e.key === 'End' ? values.length-1 : Math.max(0,Math.min(values.length-1,(i ?? values.length-1)+(e.key === 'ArrowLeft' ? -1 : 1))));}}}>
      <svg viewBox="0 0 720 216" preserveAspectRatio="none" role="img" aria-label={`${selected.label}，单位 ${selected.unit}，${numbers.length} 个区间有数据`}>
        {[16,62,108,154,200].map(n => <line key={n} x1="0" x2="720" y1={n} y2={n} className="pm-trend-grid"/>)}
        {selected.total ? values.map((n,i) => valid(n) && <rect key={i} x={x(i)-Math.max(1,704/values.length*.65)/2} y={y(n)} width={Math.max(1,704/values.length*.65)} height={Math.max(1,200-y(n))} className="pm-trend-bar"/>) : <HistoryCurve segments={paths} x={x} y={y} baseline={200}/>}
        {point && <><line x1={x(hover)} x2={x(hover)} y1="8" y2="204" className="pm-trend-cursor"/>{valid(number) && <circle cx={x(hover)} cy={y(number)} r="4" className="pm-trend-dot"/>}</>}
      </svg>
    </div></div>
    <div className="pm-trend-x">{[0,.5,1].map((part,i) => {const at=data.points[Math.round((data.points.length-1)*part)].time;return <span key={i}>{data.range === '24h' ? clock(at) : stamp(at)}</span>;})}</div>
  </div>;
}

function exportData(data,device) {
  const headers=['时间（北京时间）',...data.metrics.map(m => `${m.label}（${m.unit}）`)];
  const rows=data.points.filter(p => data.metrics.some(m => valid(p.values[m.id]))).map(p => [new Date(p.time).toLocaleString('sv-SE',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false}),...data.metrics.map(m => valid(p.values[m.id]) ? p.values[m.id] : '')]);
  const csv=[headers,...rows].map(row => row.map(value => `"${String(value).replaceAll('"','""')}"`).join(',')).join('\r\n');
  const link=document.createElement('a'), url=URL.createObjectURL(new Blob(['\ufeff',csv],{type:'text/csv;charset=utf-8'}));
  link.href=url;link.download=`${device.model}-${device.mac}-${data.range}.csv`;link.click();setTimeout(() => URL.revokeObjectURL(url),1000);
}

export function TelemetryView({devices,initialId,initialMetric,saveChartMetric}) {
  const [selected,setSelected]=useState(initialId || devices[0]?.id || ''), [range,setRange]=useState('24h');
  const d=devices.find(d => d.id === selected) || devices[0];
  return <div className="pm-telemetry"><div className="pm-telemetry-device"><Field label="历史设备"><Select value={d?.id || ''} onChange={e => setSelected(e.target.value)} disabled={!devices.length}>{!devices.length && <option value="">暂无设备</option>}{devices.map(d => <option key={d.id} value={d.id}>{d.name} · {d.model}{d.online ? '' : ' · 离线'}</option>)}</Select></Field></div><div className="pm-history-ranges" role="group" aria-label="历史时间范围">{ranges.map(([value,label]) => <button type="button" key={value} className="pm-filter" aria-pressed={range === value} onClick={() => setRange(value)}>{label}</button>)}</div>{d ? <DeviceData saveChartMetric={saveChartMetric} key={`${d.id}-${range}`} device={d} range={range} initialMetric={d.id === initialId ? initialMetric || d.chartMetric : d.chartMetric}/> : <div className="pm-empty">添加设备后会自动记录历史数据</div>}</div>;
}

function DefaultChartMetric({device:d,metrics,saveChartMetric}) {
  const [pending,setPending]=useState(null), [error,setError]=useState('');
  const lock=useRef(false);
  async function save(value) {
    if(lock.current || value === d.chartMetric) return;
    lock.current=true;setPending(value);setError('');
    try {await saveChartMetric(d.id,value);}
    catch(e) {setError(e.message);}
    finally {lock.current=false;setPending(null);}
  }
  return <div className="pm-history-default"><Field label="卡片显示曲线"><Select value={pending || d.chartMetric || metrics[0].id} onChange={e => save(e.target.value)} disabled={pending !== null} aria-busy={pending !== null} title="设备卡片与控制弹窗默认显示的指标">{metrics.map(m => <option key={m.id} value={m.id}>{m.label} · {m.unit}</option>)}</Select></Field>{error && <div className="pm-error" role="alert">{error}</div>}</div>;
}

function DeviceData({device:d,range,initialMetric,saveChartMetric}) {
  const {data,error,refresh}=useDeviceHistory(d.id,range);
  const [selected,setSelected]=useState(initialMetric || '');
  const current=data?.metrics.find(m => m.id === selected) || data?.metrics[0], stats=data?.summary[current?.id];
  const summary=d.model === 'TC1' && data ? [['区间用电',data.summary.energy.total,'度',3,'energy'],['预估花费',data.summary.cost.total,'元',2,'cost'],['平均功率',data.summary.power.avg,'W',1,'power']] : current ? [['平均值',stats.avg,current.unit,current.digits,current.id],['最低值',stats.min,current.unit,current.digits,current.id],['最高值',stats.max,current.unit,current.digits,current.id]] : [];
  return <>
    {error && <div className="pm-history-error" role="alert"><span>{error}</span><button className="pm-btn" onClick={refresh}>重试</button></div>}
    {!data && !error ? <div className="pm-empty" role="status">正在读取历史数据…</div> : data && <>
      <div className="pm-history-summary">{summary.map(([label,n,unit,digits,id]) => <div key={label}><span>{label}</span><strong><ReadingValue value={n} metricId={id} digits={digits} unit={unit}/></strong></div>)}</div>
      <div className="pm-history-chart-head"><Field label="数据指标"><Select value={current.id} onChange={e => setSelected(e.target.value)}>{data.metrics.map(m => <option key={m.id} value={m.id}>{m.label} · {m.unit}</option>)}</Select></Field><DefaultChartMetric device={d} metrics={data.metrics} saveChartMetric={saveChartMetric}/><button className="pm-text-action" disabled={!data.samples} onClick={() => exportData(data,d)}>导出 CSV</button></div>
      {data.samples ? <Trend data={data} selected={current}/> : <div className="pm-trend-empty">所选时段暂无历史数据，设备上线后会自动记录。</div>}
      {d.model === 'TC1' && <p className="pm-history-footnote">用电和花费按所选时段内的实际读数差额统计，归零后重新起算；花费沿用当时的电价估算，不追溯未计费的用电。</p>}
    </>}
  </>;
}

export function useDeviceHistory(id,range='24h') {
  const [data,setData]=useState(null), [error,setError]=useState('');
  const reload=useRef();
  useEffect(() => {
    const abort=new AbortController();let fetching=false;
    const load=async () => {
      if(fetching || document.hidden || abort.signal.aborted) return;fetching=true;
      try {const data=await api(`devices/${id}/data-history?range=${range}`,{signal:AbortSignal.any([abort.signal,AbortSignal.timeout(45000)])});if(!abort.signal.aborted) {setData(data);setError('');}}
      catch(e) {if(!abort.signal.aborted) setError(e.message);}
      finally {fetching=false;}
    };
    reload.current=load;load();const timer=setInterval(load,60000);
    document.addEventListener('visibilitychange',load);window.addEventListener('online',load);
    return () => {abort.abort();clearInterval(timer);document.removeEventListener('visibilitychange',load);window.removeEventListener('online',load);};
  },[id,range]);
  return {data:data?.deviceId === id && data.range === range ? data : null,error,refresh:() => reload.current?.()};
}
