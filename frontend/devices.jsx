import React, {useEffect, useId, useRef, useState} from 'react';
import {Icon, Select, DeviceIcon, Dot, Status, metric, when, methods, isAirMonitor, isMeteredPlug, outletCount} from './ui.jsx';
import {HistoryCurve, historyDomain, useDeviceHistory} from './telemetry.jsx';
import {deviceMetricStatus as readingStatus} from './reading-status.js';
import {ReadingValue} from './readings.jsx';

function Reading({device:d}) {
  const s = d.state || {}, status=readingStatus(d);
  if (!d.online) return <><span>—</span><span className="pm-reading-caption">等待重新连接</span></>;
  if (isMeteredPlug(d)) return <><ReadingValue metricId="power" value={s.power} unit="W" digits={1} status={status}/><span className="pm-reading-caption">{s.outlets?.filter(Boolean).length ?? '—'} / {outletCount(d)} 插口开启</span></>;
  if (isAirMonitor(d)) return <span className="pm-air-reading"><ReadingValue metricId="pm25" value={s.sensorValid ? s.pm25 : null} unit="μg/m³" status={status}/><span className="pm-reading-caption">PM2.5 · {status?.label || (s.sensorValid ? '等待读数' : '采样无效')}</span></span>;
  return <><ReadingValue metricId="speed" value={s.appliedSpeed ?? s.speed} unit="%" status={status}/><span className="pm-reading-caption">{s.on ? s.mode === 'auto' ? '自动净化' : '手动运行' : '已关闭'}{status && status.tone !== 'normal' ? ` · ${status.label}` : ''}</span></>;
}
function Toggle({device:d, action, checked, label, index, can, control}) {
  return <button type="button" className="pm-toggle" role="switch" aria-checked={!!checked} aria-label={label} data-action={action} data-index={index} disabled={!can(d,action)} onClick={() => control(d,action,action === 'outlet.set' || action === 'fan.set' ? {...(index === undefined ? {} : {index}),on:!checked} : {enabled:!checked})}/>;
}
function Slider({value, label, name, step, disabled, commit}) {
  const id = useId(), editing = useRef(false), current = useRef(value ?? 0), dirty = useRef(false);
  const [draft, setDraft] = useState(value ?? 0);
  useEffect(() => {if(!editing.current && !disabled) {setDraft(value ?? 0); current.current = value ?? 0;}},[value,disabled]);
  const finish = () => {editing.current = false; if(dirty.current) {dirty.current=false; commit(current.current);} };
  return <><label className="pm-range-label" htmlFor={id}><span>{label}</span><span data-range-value>{metric(draft)}%</span></label><input id={id} className="pm-range" type="range" aria-label={name} min="0" max="100" step={step} value={draft} disabled={disabled} onPointerDown={() => {editing.current=true;}} onChange={e => {editing.current=true; dirty.current=true; current.current=Number(e.target.value); setDraft(current.current);}} onPointerUp={finish} onKeyUp={finish} onBlur={finish} onPointerCancel={() => {editing.current=false; dirty.current=false; setDraft(value ?? 0);}}/></>;
}
function Metric({label, value, unit='', digits, status, metricId, children}) {return <div><span className="pm-metric-label">{label}</span><span className="pm-metric-value"><ReadingValue {...{value,unit,digits,status,metricId}}/></span>{children}</div>;}
function airMeasurements(d) {
  const s=d.state || {}, daikin=d.model === 'DAIKIN', value=key => daikin || s.sensorValid ? s[key] : null;
  return [
    {metricId:'pm25',label:'PM2.5',value:value('pm25'),unit:'μg/m³'},
    ...(daikin && s.pm10Present ? [{metricId:'pm10',label:'PM10',value:s.pm10,unit:'μg/m³'}] : []),
    ...(daikin ? [{metricId:'co2',label:'CO₂',value:s.co2,unit:'ppm',note:s.co2Warmup > 0 ? `预热中 · 剩余 ${Math.ceil(s.co2Warmup)} 秒` : null}] : []),
    ...(!daikin || s.hchoPresent ? [{metricId:'hcho',label:'甲醛',value:value('hcho'),unit:'mg/m³',digits:3}] : []),
    {metricId:'temp',label:'温度',value:value('temp'),unit:'°C',digits:1},
    {metricId:'humidity',label:'湿度',value:value('humidity'),unit:'%',digits:1},
    ...(daikin ? [{metricId:'tvoc',label:'TVOC',value:s.tvoc,unit:'ppb'}] : s.co2 != null ? [{metricId:'co2',label:'CO₂',value:s.co2,unit:'ppm'}] : [])
  ];
}
function AirMeasurements({device:d}) {
  return <div className="pm-measurements pm-air-measurements">{airMeasurements(d).map(({note,...item}) => <Metric key={item.metricId} {...item} status={readingStatus(d,item.metricId)}>{note && <span className="pm-metric-note">{note}</span>}</Metric>)}</div>;
}
function TileAirMeasurements({device:d}) {
  const items=airMeasurements(d).filter(item => item.metricId !== 'pm25');
  return <span className="pm-tile-air-metrics" aria-label="实时空气指标">{items.map(({metricId,label,value,unit,digits,note}) => <span key={metricId} className="pm-tile-air-metric"><span className="pm-tile-metric-label">{label}</span><span className="pm-tile-metric-value"><ReadingValue {...{metricId,unit,digits}} value={d.online ? value : null} status={readingStatus(d,metricId)}/></span>{d.online && note && <span className="pm-tile-metric-note">预热中</span>}</span>)}</span>;
}
function TileOutlets({device:d,id}) {
  return <span id={id} className="pm-tile-outlets" role="group" aria-label="插口状态">{Array.from({length:outletCount(d)},(_,index) => {
    const name=d.state?.outletNames?.[index] || `插口 ${index+1}`, value=d.state?.outlets?.[index];
    const state=!d.online ? 'offline' : value === true ? 'on' : value === false ? 'off' : 'unknown';
    const label={on:'开启',off:'关闭',offline:'离线',unknown:'未知'}[state];
    return <span key={index} className="pm-tile-outlet" data-outlet-state={state} title={`插口 ${index+1} · ${name} · ${label}`}><span className="pm-tile-outlet-index">{String(index+1).padStart(2,'0')}</span><span className="pm-tile-outlet-name">{name}</span><span className="pm-tile-outlet-state"><Dot online={state === 'on'}/>{label}</span></span>;
  })}</span>;
}
function Outlet({device:d,index,can,control}) {
  const name=d.state?.outletNames?.[index] || `插口 ${index+1}`, id=useId(), input=useRef(), trigger=useRef(), lock=useRef(false);
  const [editing,setEditing]=useState(false), [draft,setDraft]=useState(''), [saving,setSaving]=useState(false), [error,setError]=useState('');
  useEffect(() => {if(editing) {input.current?.focus();input.current?.select();}},[editing]);
  const finish=() => {setEditing(false);setError('');requestAnimationFrame(() => trigger.current?.focus({preventScroll:true}));};
  async function save(e) {
    e.preventDefault();if(lock.current || !can(d,'config.set')) return;
    const value=draft.trim();
    if(!value) {setError('请输入插口名称');return;}
    if(new TextEncoder().encode(value).length > 63) {setError('名称过长，请缩短（最多 21 个汉字或 63 个字母）');return;}
    if(value === name) {finish();return;}
    lock.current=true;setSaving(true);setError('');
    try {const result=await control(d,'config.set',{section:'socket',values:{index,name:value}});if(result) finish();}
    finally {lock.current=false;setSaving(false);}
  }
  return editing ? <form className="pm-outlet-editor" onSubmit={save} onKeyDown={e => {if(e.key === 'Escape') {e.preventDefault();e.stopPropagation();if(!saving) finish();}}}>
    <label htmlFor={id}>插口 {String(index+1).padStart(2,'0')} 名称</label><input ref={input} id={id} className="pm-input" value={draft} onChange={e => {setDraft(e.target.value);setError('');}} maxLength={63} disabled={saving} aria-invalid={!!error} aria-describedby={error ? `${id}-error` : undefined}/>
    <div className="pm-outlet-editor-actions"><span className="pm-quiet">同步到设备网页</span><button className="pm-btn" type="button" onClick={finish} disabled={saving}>取消</button><button className="pm-btn pm-btn-primary" type="submit" disabled={saving || !can(d,'config.set')}>{saving ? '正在保存…' : '保存名称'}</button></div>{error && <div id={`${id}-error`} className="pm-error" role="alert">{error}</div>}
  </form> : <div className="pm-outlet"><span className="pm-outlet-label"><button ref={trigger} type="button" className="pm-outlet-rename" aria-label={`修改插口 ${index+1} 名称`} title="修改插口名称" disabled={!can(d,'config.set')} onClick={() => {setDraft(name);setError('');setEditing(true);}}><span className="pm-outlet-name">{name}</span><Icon name="edit"/></button><span className="pm-outlet-id">{String(index+1).padStart(2,'0')}</span></span><Toggle device={d} action="outlet.set" checked={d.state?.outlets?.[index]} label={`${d.name}插口 ${index+1}`} {...{index,can,control}}/></div>;
}
function DeviceSparkline({device:d,history,detail=false}) {
  const {data,error}=useDeviceHistory(d.id);
  const metrics=data?.metrics || d.chartMetrics || [];
  const selected=metrics.find(metric => metric.id === d.chartMetric) || metrics[0] || (isMeteredPlug(d) ? {id:'power',label:'功率',unit:'W',digits:1} : isAirMonitor(d) ? {id:'pm25',label:'PM2.5',unit:'μg/m³',digits:0} : {id:'speed',label:'风机输出',unit:'%',digits:0});
  const values=data?.points.map(point => point.values[selected.id]) || [], numbers=values.filter(n => typeof n === 'number' && Number.isFinite(n));
  const domain=historyDomain(numbers.length ? numbers : [0]), floor=selected.total ? 0 : domain.low, ceiling=domain.high;
  const x=i => 4+i/Math.max(1,values.length-1)*632, y=n => 58-(n-floor)/(ceiling-floor)*50;
  const highest=selected.total ? Math.max(...numbers) : data?.summary[selected.id]?.max;
  const segments=[];let segment=[];
  values.forEach((n,i) => {if(typeof n === 'number' && Number.isFinite(n)) segment.push([i,n]);else if(segment.length) {segments.push(segment);segment=[];}});
  if(segment.length) segments.push(segment);
  return <button type="button" className={`pm-history-sparkline${detail ? ' pm-detail-sparkline' : ''}`} aria-label={`${d.name}最近24小时${selected.label}历史`} aria-haspopup="dialog" title={`查看最近 24 小时${selected.label}历史`} onClick={() => history(d.id,selected.id)}>
    <span className="pm-sparkline-heading"><span>{selected.label}{detail ? ' · 近 24 小时' : ''}</span>{!!numbers.length && <span>最高 <ReadingValue value={highest} metricId={selected.id} digits={selected.digits} unit={selected.unit}/></span>}</span>
    <span className="pm-sparkline-plot">{numbers.length ? <svg viewBox="0 0 640 64" preserveAspectRatio="none" role="img" aria-label={`最近 24 小时${selected.label}曲线`} data-history-sparkline data-power-sparkline={selected.id === 'power' ? '' : undefined}>
      {[33,58].map(n => <line key={n} x1="0" x2="640" y1={n} y2={n} className="pm-trend-grid"/>)}
      {selected.total ? values.map((n,i) => typeof n === 'number' && Number.isFinite(n) && <rect key={i} x={x(i)-Math.max(1,632/values.length*.65)/2} y={y(n)} width={Math.max(1,632/values.length*.65)} height={Math.max(1,58-y(n))} className="pm-trend-bar"/>) : <HistoryCurve segments={segments} x={x} y={y} baseline={58}/>}
    </svg> : <span className="pm-sparkline-empty">{error ? '历史数据暂不可用' : data ? `等待${selected.label}记录` : '正在读取曲线…'}</span>}</span>
    <span className="pm-sparkline-times"><span>24 小时前</span><span>现在</span></span>
  </button>;
}
function TilePower({device:d}) {
  return <span className="pm-tile-value pm-tile-power-reading"><span className="pm-tile-watts"><ReadingValue metricId="power" value={d.online ? d.state?.power : null} unit="W" digits={1} status={readingStatus(d)}/></span><span className="pm-tile-reading-divider" aria-hidden="true">/</span><span className="pm-tile-energy-reading" title="累计用电">{metric(d.state?.energy,2)}<small>度</small></span></span>;
}
function PowerOnSetting({device:d,can,control}) {
  const id=useId(), lock=useRef(false), [draft,setDraft]=useState(null), [saving,setSaving]=useState(false);
  const s=d.state || {}, modes=s.powerOnModes || [], mode=s.powerOnMode, value=draft ?? mode;
  useEffect(() => {setDraft(null);},[d.id]);
  useEffect(() => {setDraft(current => current === mode ? null : current);},[mode]);
  if(!modes.includes(mode) || !d.actions.includes('config.set')) return null;
  async function save() {
    if(lock.current || value === mode || !can(d,'config.set')) return;
    lock.current=true;setSaving(true);
    try {
      const values=s.powerOnLegacy ? {power_on:value === 'on'} : {power_on_mode:value};
      const result=await control(d,'config.set',{section:'device',values});
      if(result?.confirmed) setDraft(null);
    } finally {lock.current=false;setSaving(false);}
  }
  return <div className="pm-power-on"><div className="pm-info-row"><label htmlFor={id}>上电输出</label><div className="pm-power-on-actions"><Select id={id} value={value} disabled={saving || !can(d,'config.set')} onChange={e => setDraft(e.target.value)}>{modes.map(mode => <option key={mode} value={mode}>{{off:'上电关闭',on:'上电开启',restore:'保持断电前状态'}[mode]}</option>)}</Select><button type="button" className="pm-btn" disabled={saving || value === mode || !can(d,'config.set')} onClick={save}>{saving ? '保存中…' : '保存'}</button></div></div></div>;
}
export function Detail({device:d, can, control, settings, history, showStatus=true}) {
  const s=d.state || {}, b=d.billing, status=readingStatus(d);
  const toggle = (action,checked,label,index) => <Toggle device={d} {...{action,checked,label,index,can,control}}/>;
  const price = b?.rate == null ? '设置电价' : `当前 ${b.rate.toFixed(4).replace(/0+$/, '').replace(/\.$/, '')} 元/度${b.period === 'flat' ? '' : b.period === 'valley' ? ' · 谷时' : ' · 峰时'}`;
  return <><div className="pm-detail-content"><div className="pm-detail-heading"><div><h3 className="pm-detail-name">{d.name}</h3><span className="pm-statusline">{d.model} / {d.room} / {methods(d)}</span></div>{isAirMonitor(d) ? showStatus && <Status device={d}/> : toggle(isMeteredPlug(d) ? 'outlet.set' : 'fan.set',s.on,`${d.name}总开关`)}</div>
    {isMeteredPlug(d) ? <><div className="pm-measurements"><Metric metricId="power" label="实时功率" value={s.power} unit="W" digits={1} status={status}><button className="pm-metric-note pm-price-link" aria-label="电价设置" onClick={() => settings(d.id,'tariff')}>{price}</button></Metric><Metric label="累计用电" value={s.energy} unit="度" digits={2}>{b?.since ? <span className="pm-metric-note" data-energy-cost title={`最后计费：${when(b.lastAt)}；峰时 ${metric(b.peakEnergy,3)} 度，谷时 ${metric(b.valleyEnergy,3)} 度，统一电价 ${metric(b.flatEnergy,3)} 度；电量回退 ${b.resets} 次。峰谷按读数间隔的时间比例估算。`}>预估消费 ¥{metric(b.cost,2)}{b.mode === 'disabled' ? ' · 已暂停' : b.awaitingSample ? ' · 待新读数' : ''}</span> : b?.mode !== 'disabled' && <span className="pm-metric-note">等待首次读数开始计费</span>}</Metric>{d.model === 'S30-V1B' && <><Metric label="电压" value={s.voltage} unit="V" digits={1}/><Metric label="电流" value={s.current} unit="A" digits={3}/></>}</div>{d.model !== 'S30-V1B' && <><div className="pm-section-label"><span>插口控制</span><span>{s.outlets?.filter(Boolean).length ?? '—'} / {outletCount(d)} 已开启</span></div><div className="pm-outlets">{Array.from({length:outletCount(d)},(_,i) => <Outlet key={i} device={d} index={i} {...{can,control}}/>)}</div></>}<div className="pm-detail-options">{d.actions.includes('indicator.set') && <div className="pm-info-row"><span>指示灯</span>{toggle('indicator.set',s.light,`${d.name}指示灯`)}</div>}{d.actions.includes('child_lock.set') && <div className="pm-info-row"><span>童锁</span>{toggle('child_lock.set',s.childLock,`${d.name}童锁`)}</div>}<PowerOnSetting device={d} {...{can,control}}/></div></> : isAirMonitor(d) ? <>
      <AirMeasurements device={d}/>
      {d.model === 'M1' ? <><Slider value={s.brightness} label="屏幕亮度" name={`${d.name}屏幕亮度`} step={25} disabled={!can(d,'display.set')} commit={brightness => control(d,'display.set',{brightness})}/><div className="pm-range-ends"><span>关闭屏幕</span><span>最亮</span></div><div className="pm-info-row"><span>数据状态</span><strong>{s.sensorValid ? '采样有效' : '等待有效采样'}</strong></div></> : d.actions.includes('indicator.set') && <div className="pm-info-row"><span>空气质量指示灯</span>{toggle('indicator.set',s.light,`${d.name}指示灯`)}</div>}
    </> : <><div className="pm-section-label"><span>运行模式</span><span>{s.fanState === 'idle' ? '自动待机' : s.on ? '运行中' : '已关闭'}</span></div><div className="pm-modes">{['auto','manual'].map(mode => <button key={mode} className="pm-mode" aria-pressed={s.mode === mode} disabled={!can(d,'fan.mode.set')} onClick={() => control(d,'fan.mode.set',{mode})}>{mode === 'auto' ? '自动净化' : '手动调速'}</button>)}</div><Slider value={s.mode === 'auto' ? s.autoMaxSpeed : s.requestedSpeed} label={s.mode === 'auto' ? '自动风量上限' : '风机转速'} name={`${d.name}风量`} step={5} disabled={!can(d,'fan.set')} commit={speed => control(d,'fan.set',{speed,mode:s.mode})}/><div className="pm-info-row"><span>实际输出</span><strong><ReadingValue metricId="speed" value={s.appliedSpeed ?? s.speed} unit="%" status={status}/></strong></div><div className="pm-info-row"><span>灯环</span>{toggle('light.set',s.light,`${d.name}灯环`)}</div><div className="pm-info-row"><span>滤芯剩余</span><strong><ReadingValue metricId="filter" value={s.filter} unit="%" status={readingStatus(d,'filter')}/>{s.filterDue ? ' · 建议更换' : ''}</strong></div></>}
    {!isMeteredPlug(d) && d.actions.includes('child_lock.set') && <div className="pm-info-row"><span>童锁</span>{toggle('child_lock.set',s.childLock,`${d.name}童锁`)}</div>}
    </div>
    <DeviceSparkline key={d.id} device={d} history={history} detail/>
  </>;
}
function RoomTile({device:d,openControl,...controls}) {
  const outletsId=useId();
  return <article className={`pm-panel pm-tile${isMeteredPlug(d) ? ' pm-tile-tc1' : isAirMonitor(d) ? ' pm-tile-air' : ''}`}>
    <div className="pm-tile-head"><div className="pm-tile-head-controls">
      <button type="button" className="pm-tile-icon-button" aria-label={`${d.name}设备控制`} aria-haspopup="dialog" title="打开设备控制" onClick={() => openControl(d.id)}><span className="pm-device-icon"><DeviceIcon device={d}/></span></button>
      <span className="pm-tile-status"><Status device={d}/><span className="pm-tile-model">{d.model}</span></span>
    </div>{!isAirMonitor(d) && <div className="pm-tile-master"><span className="pm-quiet">总开关</span><Toggle device={d} action={isMeteredPlug(d) ? 'outlet.set' : 'fan.set'} checked={d.state?.on} label={`${d.name}总开关`} {...controls}/></div>}</div>
    <button type="button" className="pm-tile-open" data-open-control={d.id} aria-label={`打开${d.name}控制`} aria-describedby={d.model === 'TC1' ? outletsId : undefined} aria-haspopup="dialog" onClick={() => openControl(d.id)}>
      {isMeteredPlug(d) ? <span className="pm-tile-primary"><span className="pm-tile-info"><span className="pm-tile-name" title={d.name}>{d.name}</span></span><TilePower device={d}/></span> : <><span className="pm-tile-info"><span className="pm-tile-name">{d.name}</span></span><span className="pm-tile-value"><Reading device={d}/></span></>}
      {d.model === 'TC1' && <TileOutlets device={d} id={outletsId}/>}
      {d.model === 'S30-V1B' && <span className="pm-tile-air-metrics" aria-label="实时电气指标">{[['voltage','电压','V',1],['current','电流','A',3]].map(([key,label,unit,digits]) => <span key={key} className="pm-tile-air-metric"><span className="pm-tile-metric-label">{label}</span><span className="pm-tile-metric-value"><ReadingValue value={d.online ? d.state?.[key] : null} unit={unit} digits={digits}/></span></span>)}</span>}
      {isAirMonitor(d) && <TileAirMeasurements device={d}/>}
    </button>
    <DeviceSparkline device={d} history={controls.history}/>
  </article>;
}
function deviceFilters(devices) {
  return ['all',...['TC1','M1','A1','DAIKIN','S30-V1B'].filter(model => devices.some(d => d.model === model))];
}
export function DeviceToolbar({devices, layout, filter, setFilter, setLayout, add}) {
  const filters=deviceFilters(devices), activeFilter=filters.includes(filter) ? filter : 'all';
  const addButton=<button className="pm-filter pm-add-device" type="button" aria-label="添加设备" title="添加设备" onClick={add}><Icon name="plus"/><span>添加设备</span></button>;
  return <div className="pm-device-toolbar"><div className="pm-filterbar" aria-label="按型号筛选">{filters.map(f => {
    const label=f === 'all' ? '全部设备' : f === 'DAIKIN' ? '大金' : f, count=f === 'all' ? devices.length : devices.filter(d => d.model === f).length;
    return <button key={f} className="pm-filter" aria-description={`${count} 台设备`} aria-pressed={activeFilter === f} onClick={() => setFilter(f)}><span>{label}</span><span className="pm-filter-count" aria-hidden="true">{count}</span></button>;
  })}</div>{addButton}<div className="pm-view-switch" role="group" aria-label="显示方式">{[['rooms','空间','grid'],['list','列表','list']].map(([view,label,icon]) => <button key={view} className="pm-view-option" aria-label={label} title={label} aria-pressed={layout === view} onClick={() => setLayout(view)}><Icon name={icon}/></button>)}</div></div>;
}
export function DeviceWorkspace({devices, selected, select, layout, filter, setFilter, loading, openControl, ...controls}) {
  const filters=deviceFilters(devices), activeFilter=filters.includes(filter) ? filter : 'all';
  useEffect(() => {if(filter !== activeFilter) setFilter(activeFilter);},[filter,activeFilter,setFilter]);
  const ds=devices.filter(d => activeFilter === 'all' || d.model === activeFilter), d=ds.find(d => d.id === selected) || ds[0];
  const groups=[...new Set(ds.map(d => d.room))].map(room => ds.filter(d => d.room === room)), ordered=[];
  for(let i=0;i<Math.max(0,...groups.map(g => g.length));i++) groups.forEach(g => {if(g[i]) ordered.push(g[i]);});
  return <>
    {layout === 'list' ? <div className="pm-workspace"><section className="pm-panel pm-list-panel"><div className="pm-panel-head"><h2 className="pm-panel-title"><span className="pm-index">01</span>设备列表</h2><span className="pm-quiet">{ds.length} 台设备</span></div>{!!ds.length && <div className="pm-mobile-picker"><Select aria-label="选择设备" value={d.id} onChange={e => select(e.target.value)}>{ds.map(x => <option key={x.id} value={x.id}>{x.name} · {x.online ? '在线' : '离线'}</option>)}</Select></div>}<div className="pm-device-list">{ds.length ? ds.map(x => <button key={x.id} className="pm-row" data-device={x.id} aria-pressed={x.id === d.id} aria-label={`查看${x.name}`} onClick={() => select(x.id)}><span className="pm-device-icon"><DeviceIcon device={x}/></span><span><span className="pm-device-name">{x.name}</span><span className="pm-device-meta"><Dot online={x.online}/>{x.online ? '在线' : '离线'} / {x.model} / {methods(x)}</span></span><span className="pm-device-reading"><Reading device={x}/></span></button>) : <div className="pm-empty">{loading ? '正在读取设备…' : '还没有设备，请从上方添加'}</div>}</div></section><section className="pm-panel"><div className="pm-panel-head"><h2 className="pm-panel-title"><span className="pm-index">02</span>快速控制</h2>{d && <Status device={d}/>}</div><div className="pm-detail" key={d?.id || 'empty'}>{d ? <Detail key={d.id} device={d} showStatus={false} {...controls}/> : <div className="pm-empty">添加设备后即可查看和控制</div>}</div></section></div> : <><div className="pm-room-grid">{ordered.length ? ordered.map(x => <section className="pm-room-item" key={x.id}><div className="pm-room-header">{x.room}<span>{ds.filter(y => y.room === x.room).length} 台设备</span></div><RoomTile device={x} openControl={openControl} {...controls}/></section>) : <div className="pm-empty">还没有设备</div>}</div></>}
  </>;
}
