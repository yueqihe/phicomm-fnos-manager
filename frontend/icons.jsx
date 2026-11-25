import React from 'react';
import {Settings2, X, Plus, List, LayoutGrid, PlugZap, AirVent, Wind, Unplug, ArrowUpRight, Pencil, Gauge, Network, RefreshCw, Zap, Palette, Download, Info, Power, GripVertical, Check, ChevronDown} from 'lucide-react';

const icons = {settings: Settings2, x: X, plus: Plus, list: List, grid: LayoutGrid, plug: PlugZap, air: AirVent, wind: Wind, unplug: Unplug, arrow: ArrowUpRight, edit: Pencil, gauge: Gauge, service: Network, refresh: RefreshCw, tariff: Zap, appearance: Palette, export: Download, about: Info, power: Power, grip: GripVertical, check: Check, chevron: ChevronDown};

export function Icon({name,className='',...props}) {
  const Component = icons[name];
  return <Component size={17} strokeWidth={1.6} aria-hidden="true" className={className ? `pm-icon ${className}` : 'pm-icon'} data-icon={name} {...props}/>;
}
