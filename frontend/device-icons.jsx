import React from 'react';

// Device silhouettes adapt Lucide's ISC-licensed icons. Animate the working
// parts while keeping the outline, size and color consistent across views.
const names={TC1:'plug','S30-V1B':'power',M1:'air',DAIKIN:'gauge',A1:'wind'};

function Shape({model}) {
  if(model === 'TC1') return <>
    <path d="M6.3 20.3a2.4 2.4 0 0 0 3.4 0L12 18l-6-6-2.3 2.3a2.4 2.4 0 0 0 0 3.4Z"/>
    <path d="m2 22 3-3M7.5 13.5 10 11M10.5 16.5 13 14"/>
    <path className="pm-glyph-charge" d="m18 3-4 4h6l-4 4"/>
  </>;
  if(model === 'S30-V1B') return <>
    <path className="pm-glyph-power-ring" d="M18.36 6.64a9 9 0 1 1-12.73 0"/>
    <path d="M12 2v10"/>
    <path className="pm-glyph-energy" pathLength="100" d="M18.36 6.64a9 9 0 1 1-12.73 0"/>
  </>;
  if(model === 'M1') return <>
    <path d="M6 12H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-2M6 8h12"/>
    <path className="pm-glyph-flow" d="M18 17.5a2.5 2.5 0 1 1-4 2.03V12"/>
    <path className="pm-glyph-flow pm-glyph-flow-second" d="M6.6 15.572A2 2 0 1 0 10 17v-5"/>
  </>;
  if(model === 'DAIKIN') return <>
    <path d="M3.34 19a10 10 0 1 1 17.32 0"/>
    <g className="pm-glyph-needle"><path className="pm-glyph-scan" d="M12 14V7"/></g>
    <circle cx="12" cy="14" r="1.1" fill="currentColor" stroke="none"/>
  </>;
  if(model === 'A1') return <>
    <g className="pm-glyph-rotor"><path d="M10.827 16.379a6.082 6.082 0 0 1-8.618-7.002l5.412 1.45a6.082 6.082 0 0 1 7.002-8.618l-1.45 5.412a6.082 6.082 0 0 1 8.618 7.002l-5.412-1.45a6.082 6.082 0 0 1-7.002 8.618l1.45-5.412Z"/></g>
    <circle cx="12" cy="12" r="1" fill="currentColor" stroke="none"/>
  </>;
  return <path d="M9 3v4m6-4v4M6 7h12v4a6 6 0 0 1-12 0V7Zm6 10v4"/>;
}

export function DeviceIcon({device:d}) {
  const s=d.state || {}, monitor=['M1','DAIKIN'].includes(d.model);
  const metered=['TC1','S30-V1B'].includes(d.model), speed=s.appliedSpeed ?? s.speed;
  const sampling=monitor && (d.model === 'M1' ? s.sensorValid === true : ['pm25','pm10','co2','temp','humidity','tvoc','hcho'].some(key => typeof s[key] === 'number' && Number.isFinite(s[key])));
  const operating=d.model === 'A1' ? s.on && speed > 0 && s.fanState !== 'idle' : metered && (s.on || s.outlets?.some(Boolean));
  const active=!!(d.online && (monitor ? sampling : operating));
  const angle=typeof s.pm25 === 'number' && Number.isFinite(s.pm25) ? Math.max(-60,Math.min(60,s.pm25/150*120-60)) : 0;
  return <svg className="pm-icon pm-device-glyph" data-icon={names[d.model] || 'plug'} data-model={d.model} data-icon-online={!!d.online} data-icon-active={active} aria-hidden="true" width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" style={{'--pm-rotor-duration':`${4.5-Math.max(0,Math.min(100,Number(speed)||0))*.035}s`,'--pm-gauge-angle':`${angle}deg`}}><Shape model={d.model}/></svg>;
}
