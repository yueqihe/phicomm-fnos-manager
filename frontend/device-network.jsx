import React from 'react';
import {Icon, methods} from './ui.jsx';

function settingsURL(device) {
  const ip=device.state?.network?.ip;
  const address=device.address || (ip ? `http://${ip.includes(':') ? `[${ip}]` : ip}` : '');
  if(!address) return '';
  try {
    const url=new URL(address.includes('://') ? address : `http://${address}`);
    if(!['http:','https:'].includes(url.protocol) || url.username || url.password) return '';
    url.pathname='/';url.search='';url.hash='settings/device';
    return url.href;
  } catch {return '';}
}

export function DeviceNetwork({device:d}) {
  const network=d.state?.network || {}, href=settingsURL(d), mac=/^[a-f0-9]{12}$/i.test(d.mac) ? d.mac.match(/.{2}/g).join(':').toUpperCase() : '';
  const ssid=network.connected === false ? `${network.ssid ? `${network.ssid} · ` : ''}未连接` : network.ssid;
  const rows=[['Wi-Fi',ssid],['信号强度',typeof network.rssi === 'number' ? `${network.rssi} dBm` : null],['IP 地址',network.ip],['MAC 地址',mac],['子网掩码',network.mask],['默认网关',network.gateway],['接入方式',methods(d)],['固件版本',d.state?.version]];
  return <section className="pm-device-network" aria-label="设备网络信息">
    <div className="pm-device-network-heading"><h3>设备信息</h3>{!d.online && <span>最近上报信息</span>}</div>
    <dl>{rows.map(([label,value]) => <div key={label}><dt>{label}</dt><dd>{value || '未上报'}</dd></div>)}</dl>
    {href ? <a className="pm-btn pm-device-settings-link" href={href} target="_blank" rel="noopener noreferrer" aria-label="打开设备高级设置（新标签页）">设备高级设置<Icon name="arrow"/></a> : <button className="pm-btn" type="button" disabled>设备高级设置<Icon name="arrow"/></button>}
    <p className="pm-help">{href ? '需在能访问设备局域网的网络中打开。' : '设备尚未上报可访问的内网地址。'}</p>
  </section>;
}
