const finite=value => typeof value === 'number' && Number.isFinite(value);
const status=(tone,label,hint) => ({tone,label,hint});

// Display ranges use each metric's reported unit. In particular, TVOC is ppb;
// it must not be compared with a mass concentration in mg/m³.
export function metricStatus(id,value) {
  if(!finite(value) || id !== 'temp' && value<0) return null;
  switch(id) {
    case 'pm25':
      return status(value<=35 ? 'good' : value<=75 ? 'warning' : 'high',value<=35 ? '优' : value<=75 ? '良' : value<=115 ? '轻度污染' : value<=150 ? '中度污染' : value<=250 ? '重度污染' : '严重污染','PM2.5 读数提示：35 μg/m³ 及以下为绿色，75 μg/m³ 及以下为橙色，更高为红色');
    case 'pm10':
      return status(value<=50 ? 'good' : value<=150 ? 'warning' : 'high',value<=50 ? '较低' : value<=150 ? '偏高' : '较高','PM10 读数提示：50 μg/m³ 及以下为绿色，150 μg/m³ 及以下为橙色，更高为红色');
    case 'co2':
      return status(value<=1000 ? 'good' : value<=1500 ? 'warning' : 'high',value<=1000 ? '通风良好' : value<=1500 ? '偏高' : '建议通风','CO₂ 读数提示：1000 ppm 及以下为绿色，1500 ppm 及以下为橙色，更高为红色');
    case 'hcho':
      return status(value<=.08 ? 'good' : value<=.1 ? 'warning' : 'high',value<=.08 ? '较低' : value<=.1 ? '偏高' : '较高','甲醛读数提示：0.08 mg/m³ 及以下为绿色，0.10 mg/m³ 及以下为橙色，更高为红色');
    case 'tvoc':
      return status(value<=220 ? 'good' : value<=660 ? 'warning' : 'high',value<=220 ? '较低' : value<=660 ? '偏高' : '较高','TVOC 读数提示：220 ppb 及以下为绿色，660 ppb 及以下为橙色，更高为红色');
    case 'temp':
      return status(value>=18 && value<=28 ? 'good' : value>=10 && value<=32 ? 'warning' : 'high',value<18 ? '偏冷' : value>28 ? '偏热' : '舒适','温度舒适提示：18–28 °C 为绿色，低于 10 °C 或高于 32 °C 为红色，其余为橙色');
    case 'humidity':
      if(value>100) return null;
      return status(value>=40 && value<=60 ? 'good' : value>=30 && value<=70 ? 'warning' : 'high',value<40 ? '偏干' : value>60 ? '偏湿' : '舒适','湿度舒适提示：40%–60% 为绿色，低于 30% 或高于 70% 为红色，其余为橙色');
    case 'power':
      return status(value>=2000 ? 'high' : value>=1000 ? 'warning' : 'normal',value>=2000 ? '高功耗' : value>=1000 ? '功耗偏高' : '正常功耗','功耗提示：1000 W 起为橙色，2000 W 起为红色');
    case 'speed':
      if(value>100) return null;
      return status(value>=85 ? 'high' : value>=60 ? 'warning' : 'normal',value>=85 ? '高转速' : value>=60 ? '转速偏高' : '正常转速','实际风机输出：60% 起为橙色，85% 起为红色');
    case 'filter':
      if(value>100) return null;
      return status(value>20 ? 'good' : value>10 ? 'warning' : 'high',value>20 ? '余量充足' : value>10 ? '余量偏低' : '建议更换','滤芯余量提示：高于 20% 为绿色，10% 及以下为红色，其余为橙色');
    default:
      return null;
  }
}

export function deviceMetricStatus(device,id) {
  if(!device.online) return null;
  const s=device.state || {};
  id ||= device.model === 'TC1' ? 'power' : ['M1','DAIKIN'].includes(device.model) ? 'pm25' : 'speed';
  if((device.model === 'M1' && id !== 'co2' && !s.sensorValid) || (id === 'speed' && !s.on)) return null;
  const value=id === 'speed' ? s.appliedSpeed ?? s.speed : s[id], result=metricStatus(id,value);
  if(id === 'filter' && result && s.filterDue) return status('high','建议更换','设备已发出滤芯更换提示');
  return result;
}
