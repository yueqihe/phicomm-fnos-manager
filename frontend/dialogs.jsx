import React, {useEffect, useRef, useState} from 'react';
import {api, json, Modal, Field, Input, RoomField, Status, when} from './ui.jsx';
import {Discovery} from './discovery.jsx';

const ConnectionMethod = ({value,onChange,adding=false}) => <Field label="接入方式"><select className="pm-input" name="method" value={value} onChange={onChange}><option value="hybrid">局域网 + MQTT{adding ? '' : '（局域网优先）'}</option><option value="lan">{adding ? '局域网直连' : '仅局域网直连'}</option><option value="mqtt">{adding ? 'MQTT 接入' : '仅 MQTT 接入'}</option></select></Field>;
export function AddDialog({devices,onClose,added,message}) {
  const [values,setValues]=useState({method:'hybrid',name:'',model:'TC1',room:'未分组',address:'',password:'',mac:''}), [busy,setBusy]=useState(false), [error,setError]=useState('');
  const form=useRef();
  const change=e => setValues(v => ({...v,[e.target.name]:e.target.value}));
  const choose=d => {setValues(v => ({...v,method:'hybrid',address:d.address,model:d.model || v.model,name:v.name || (d.model ? `${d.model} · ${d.mac.slice(-4)}` : '')}));requestAnimationFrame(() => form.current?.elements[d.protected ? 'password' : 'name']?.focus());};
  async function submit(e) {
    e.preventDefault(); if(busy) return; setBusy(true); setError('');
    try {const data=await api('devices',json('POST',values)); await added(data.device);}
    catch(e) {setError(e.message);}
    finally {setBusy(false);}
  }
  return <Modal name="add" title="添加设备" {...{onClose,message}} footer={<div className="pm-dialog-footer"><button className="pm-btn" onClick={onClose}>取消</button><button className="pm-btn pm-btn-primary" type="submit" form="add-form" disabled={busy}>{busy ? '正在接入…' : '添加设备'}</button></div>}><form ref={form} id="add-form" className="pm-dialog-body" onSubmit={submit}>{values.method !== 'mqtt' && <Discovery choose={choose}/>}<ConnectionMethod value={values.method} onChange={change} adding/><Field label="设备名称"><Input name="name" value={values.name} onChange={change} required maxLength={48} placeholder="例如：客厅排插"/></Field><Field label="设备型号"><select className="pm-input" name="model" value={values.model} onChange={change}>{['TC1','M1','A1'].map(model => <option key={model}>{model}</option>)}</select></Field><RoomField value={values.room} onChange={change} devices={devices}/>{values.method !== 'mqtt' ? <div data-lan-fields><Field label="设备内网地址"><Input name="address" value={values.address} onChange={change} required placeholder="192.168.1.100 或主机名:端口" autoCapitalize="none"/></Field><Field label="设备网页密码"><Input name="password" value={values.password} onChange={change} type="password" maxLength={32} autoComplete="off" placeholder="未设置可留空"/></Field></div> : <div data-mqtt-fields><Field label="设备 MAC"><Input name="mac" value={values.mac} onChange={change} required placeholder="aabbcc112233" pattern="[a-fA-F0-9:-]{12,17}"/></Field><p className="pm-help">MQTT v2 设备连接后会自动出现，也可填写 MAC 提前登记。在设备端使用服务设置中的 Broker 和账号。</p></div>}<div className="pm-error" role="alert">{error}</div></form></Modal>;
}
function TariffFields({value:v,change}) {
  const price=(label,name,active) => <Field label={`${label}（元/度）`}><Input name={name} value={v[name] ?? ''} onChange={change} type="number" min="0" max="1000" step="0.0001" inputMode="decimal" required={active} placeholder="填写实际电价"/></Field>;
  return <><Field label="计费方式"><select className="pm-input" name="mode" value={v.mode} onChange={change}><option value="disabled">暂不计费</option><option value="flat">统一电价</option><option value="tou">峰谷电价</option></select></Field><fieldset className="pm-tariff-fields" hidden={v.mode !== 'flat'} disabled={v.mode !== 'flat'}>{price('当前电价','flatRate',v.mode === 'flat')}</fieldset><fieldset className="pm-tariff-fields" hidden={v.mode !== 'tou'} disabled={v.mode !== 'tou'}>{price('峰时电价','peakRate',v.mode === 'tou')}{price('谷时电价','valleyRate',v.mode === 'tou')}<div className="pm-time-fields"><Field label="谷时开始"><Input name="valleyStart" value={v.valleyStart} onChange={change} type="time" step="60" required={v.mode === 'tou'}/></Field><Field label="谷时结束"><Input name="valleyEnd" value={v.valleyEnd} onChange={change} type="time" step="60" required={v.mode === 'tou'}/></Field></div><p className="pm-help">按北京时间（UTC+8）每日重复；支持跨午夜，谷时以外均按峰时计费。</p></fieldset><p className="pm-help pm-tariff-help">1 度 = 1 kWh。保存后从首次有效读数开始记录，不追溯历史用电。改价保留已记录电费，并从新读数重新起算；暂停期间不计费。峰谷电量按两次读数间的时间比例分摊，离线期间同样估算，电费仅供参考。</p></>;
}
export function SettingsDialog({devices,settings,initialId,initialTab='devices',onClose,sync,message,notify,busy:deviceBusy,theme,setTheme}) {
  const [tab,setTab]=useState(initialTab), [selected,setSelected]=useState(initialId || devices[0]?.id || ''), [drafts,setDrafts]=useState({}), [saving,setSaving]=useState(false), [error,setError]=useState(''), [saved,setSaved]=useState(''), [confirm,setConfirm]=useState('');
  const form=useRef(), lock=useRef(false);
  const d=devices.find(d => d.id === selected), c=settings.mqtt;
  const key=tab === 'devices' ? selected : tab;
  const defaults=tab === 'devices' ? {name:d?.name || '',room:d?.room || '未分组',method:d?.method || 'hybrid',address:d?.address || '',password:'',clearPassword:false} : tab === 'tariff' ? settings.tariff : {...c,password:'',clearPassword:false};
  const v=drafts[key] || defaults;
  const change=e => {const {name,value,type,checked}=e.target;setDrafts(old => ({...old,[key]:{...v,[name]:type === 'checkbox' ? checked : value}}));setSaved('');};
  const switchTab=value => {if(saving) return;setTab(value);setError('');setSaved('');setConfirm('');};
  async function save(e) {
    e?.preventDefault(); if(lock.current || !form.current?.reportValidity()) return;
    lock.current=true;setSaving(true);setError('');setSaved('');
    try {
      const body={...v};
      if(tab === 'tariff') {if(body.mode === 'flat') {delete body.peakRate;delete body.valleyRate;delete body.valleyStart;delete body.valleyEnd;} else if(body.mode === 'tou') delete body.flatRate;else {for(const name of ['flatRate','peakRate','valleyRate','valleyStart','valleyEnd']) delete body[name];}}
      await api(tab === 'devices' ? `devices/${selected}` : `settings/${tab === 'tariff' ? 'tariff' : 'mqtt'}`,json('PUT',body));
      await sync(true);setDrafts(old => {const next={...old};delete next[key];return next;});setSaved('配置已保存');
    } catch(e) {setError(e.message);}
    finally {lock.current=false;setSaving(false);}
  }
  async function deviceAction(action) {
    if(confirm !== action) {setConfirm(action);return;}
    if(lock.current) return;lock.current=true;setSaving(true);setError('');
    try {
      if(action === 'remove') {await api(`devices/${d.id}`,{method:'DELETE'});await sync(true);setSelected(devices.find(x => x.id !== d.id)?.id || '');}
      else {const data=await api(`devices/${d.id}/mqtt-config`,json('POST',{}));notify(data.message);await sync(true);}
      setConfirm('');
    } catch(e) {setError(e.message);}
    finally {lock.current=false;setSaving(false);}
  }
  return <Modal name="settings" title="设置" {...{onClose,message}} footer={<div className="pm-dialog-footer"><span className="pm-save-status" role="status">{tab === 'appearance' ? '外观即时生效' : saved}</span><button className="pm-btn" onClick={onClose}>关闭</button>{tab !== 'appearance' && <button className="pm-btn pm-btn-primary" disabled={saving || tab === 'devices' && !d} onClick={save}>{saving ? '正在保存…' : '保存配置'}</button>}</div>}><nav className="pm-settings-tabs" aria-label="设置分类">{[['devices','设备连接'],['service','服务设置'],['tariff','电价设置'],['appearance','外观']].map(([value,label]) => <button className="pm-settings-tab" key={value} aria-pressed={tab === value} disabled={saving} onClick={() => switchTab(value)}>{label}</button>)}</nav><div className="pm-dialog-body">{tab === 'appearance' ? <><Field label="主题"><select className="pm-input" value={theme} onChange={e => setTheme(e.target.value)}><option value="system">跟随系统</option><option value="light">浅色</option><option value="dark">深色</option></select></Field><p className="pm-help">自动记住此设备的外观偏好；跟随系统时，随系统的浅色或深色模式切换。</p></> : tab === 'devices' && !d ? <div className="pm-empty">添加设备后可在此配置连接</div> : <form ref={form} onSubmit={save} key={key} {...{[tab === 'devices' ? 'data-device-form' : tab === 'tariff' ? 'data-tariff-form' : 'data-service-form']:true}}>
    {tab === 'tariff' ? <TariffFields value={v} change={change}/> : tab === 'service' ? <><Field label="MQTT 服务"><select className="pm-input" name="mode" value={v.mode} onChange={change}><option value="disabled">不启用</option><option value="embedded">内置 MQTT 服务</option><option value="external">连接外部 Broker</option></select></Field><Field label="Broker 地址 / NAS 内网地址"><Input name="host" value={v.host} onChange={change} placeholder="设备可访问的 IP 或主机名" maxLength={253}/></Field><Field label="端口"><Input name="port" value={v.port} onChange={change} type="number" min="1" max="65535" required/></Field><Field label="接入账号"><Input name="username" value={v.username} onChange={change} maxLength={63} autoComplete="off"/></Field><Field label="接入密码"><Input name="password" value={v.password} onChange={change} type="password" autoComplete="new-password" maxLength={256} disabled={v.clearPassword} placeholder={c.passwordSet ? '留空保留已保存的密码' : '内置服务需设置密码'}/></Field><label className="pm-check" hidden={!c.passwordSet}><input type="checkbox" name="clearPassword" checked={v.clearPassword} onChange={change}/>清除已保存密码</label><label className="pm-check"><input type="checkbox" name="tls" checked={v.tls} onChange={change}/>外部 Broker 使用 TLS</label><p className="pm-help" data-mqtt-status>{settings.mqttStatus.message}</p><p className="pm-help">内置服务直接运行于 NAS；设备需使用同一账号和 TMCP v2。外部 Broker 需允许该账号的设备主题。更改服务会重新建立 MQTT 连接。</p></> : <><Field label="设备"><select className="pm-input" value={selected} onChange={e => {setSelected(e.target.value);setError('');setSaved('');setConfirm('');}}>{devices.map(x => <option value={x.id} key={x.id}>{x.name} · {x.model}</option>)}</select></Field><div className="pm-config-identity"><span className="pm-mono">{d.model} · {d.mac}</span><Status device={d}/></div><Field label="设备名称"><Input name="name" value={v.name} onChange={change} required maxLength={48}/></Field><RoomField value={v.room} onChange={change} devices={devices}/><ConnectionMethod value={v.method} onChange={change}/><section data-config-lan hidden={v.method === 'mqtt'}><Field label="设备内网地址"><Input name="address" value={v.address} onChange={change} required={v.method !== 'mqtt'} placeholder="192.168.1.100"/></Field><Field label="设备网页密码"><Input name="password" value={v.password} onChange={change} type="password" autoComplete="off" maxLength={32} disabled={v.clearPassword} placeholder={d.passwordSet ? '留空保留已保存的密码' : '未设置可留空'}/></Field><label className="pm-check" hidden={!d.passwordSet}><input type="checkbox" name="clearPassword" checked={v.clearPassword} onChange={change}/>清除已保存密码</label></section><section className="pm-config-section"><h3>MQTT 接入</h3><p className="pm-help">{settings.mqttStatus.message}{c.host ? ` · ${c.host}:${c.port}` : ''}</p><p className="pm-help">服务设置填写 NAS 地址和账号后，可将 MQTT 配置写入设备。TC1 会切换到 TMCP v2。</p><button className="pm-btn" type="button" disabled={saving || !d.online || deviceBusy.has(d.id) || c.mode === 'disabled'} onClick={() => deviceAction('provision')}>{confirm === 'provision' ? '确认写入并重连 MQTT' : '写入当前 MQTT 服务配置'}</button></section></>}
    <div className="pm-error" role="alert">{error}</div>{tab === 'devices' && <div className="pm-remove-area"><button className="pm-text-action is-danger" type="button" disabled={saving} onClick={() => deviceAction('remove')}>{confirm === 'remove' ? '确认移除（不重置设备）' : '移除此设备'}</button></div>}</form>}</div></Modal>;
}
export function HistoryDialog({onClose,message}) {
  const [rows,setRows]=useState([]), [next,setNext]=useState(null), [loading,setLoading]=useState(true), [error,setError]=useState('');
  const alive=useRef(true), lock=useRef(false);
  async function load(before) {
    if(lock.current) return;lock.current=true;setLoading(true);
    try {const data=await api(`history${before ? `?before=${before}` : ''}`);if(alive.current) {setRows(old => before ? [...old,...data.records] : data.records);setNext(data.next);setError('');}}
    catch(e) {if(alive.current) setError(e.message);}
    finally {lock.current=false;if(alive.current) setLoading(false);}
  }
  useEffect(() => {alive.current=true;load();return () => {alive.current=false;};},[]);
  const outcomes={confirmed:'已确认',accepted:'待确认',unconfirmed:'未确认',failed:'失败',ok:'完成'};
  return <Modal name="history" title="历史记录" {...{onClose,message}} footer={<div className="pm-dialog-footer">{next && <button className="pm-btn" disabled={loading} onClick={() => load(next)}>加载更早记录</button>}<button className="pm-btn" onClick={onClose}>关闭</button></div>}><div className="pm-dialog-body"><div className="pm-activity">{rows.map(e => <div className="pm-event" key={e.id}><time>{when(e.time)}</time><span>{e.name}<br/><span className="pm-quiet">{e.message} · {e.action}</span></span><span className="pm-quiet">{outcomes[e.outcome] || e.outcome}</span></div>)}{!rows.length && <div className="pm-empty">{loading ? '正在读取…' : '暂无活动记录'}</div>}<div className="pm-error" role="alert">{error}</div></div></div></Modal>;
}
