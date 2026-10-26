import React from 'react';
import {metric} from './ui.jsx';
import {metricStatus} from './reading-status.js';

export function ReadingValue({value,unit='',digits,status,metricId}) {
  const valid=typeof value === 'number' && Number.isFinite(value);
  const current=valid ? (status === undefined ? metricStatus(metricId,value) : status) : null;
  return <span className="pm-live-reading" data-metric={metricId} data-reading-tone={current?.tone} title={current ? `${current.label} · ${current.hint}` : undefined}>{metric(value,digits)}<small>{unit}</small>{current && <span className="pm-sr-only">（{current.label}）</span>}</span>;
}
