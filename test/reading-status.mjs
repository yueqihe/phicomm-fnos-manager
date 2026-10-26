import assert from 'node:assert/strict';
import test from 'node:test';
import {metricStatus,deviceMetricStatus} from '../frontend/reading-status.js';

test('pollution indicators classify exact boundaries in the reported units',() => {
  for(const [id,green,orange] of [['pm25',35,75],['pm10',50,150],['co2',1000,1500],['hcho',.08,.1],['tvoc',220,660]]) {
    for(const [value,tone] of [[0,'good'],[green,'good'],[green+(orange-green)/2,'warning'],[orange,'warning'],[orange+1,'high']]) {
      assert.equal(metricStatus(id,value)?.tone,tone,`${id}: ${value}`);
    }
  }
  assert.equal(metricStatus('hcho',.101).tone,'high');
  assert.equal(metricStatus('tvoc',.66).tone,'good','ppb must not be compared to a mg/m³ cutoff');
});

test('temperature and humidity warn on both sides of the comfort range',() => {
  for(const [id,cases] of [
    ['temp',[[-5,'high'],[9.9,'high'],[10,'warning'],[17.9,'warning'],[18,'good'],[28,'good'],[28.1,'warning'],[32,'warning'],[32.1,'high']]],
    ['humidity',[[0,'high'],[29.9,'high'],[30,'warning'],[39.9,'warning'],[40,'good'],[60,'good'],[60.1,'warning'],[70,'warning'],[70.1,'high']]]
  ]) for(const [value,tone] of cases) assert.equal(metricStatus(id,value)?.tone,tone,`${id}: ${value}`);
  assert.equal(metricStatus('temp',0).label,'偏冷');
  assert.equal(metricStatus('humidity',80).label,'偏湿');
});

test('power, speed and filter thresholds keep their respective meanings',() => {
  for(const [id,cases] of [
    ['power',[[0,'normal'],[999,'normal'],[1000,'warning'],[1999,'warning'],[2000,'high']]],
    ['speed',[[0,'normal'],[59,'normal'],[60,'warning'],[84,'warning'],[85,'high']]],
    ['filter',[[0,'high'],[10,'high'],[11,'warning'],[20,'warning'],[21,'good'],[100,'good']]]
  ]) for(const [value,tone] of cases) assert.equal(metricStatus(id,value)?.tone,tone,`${id}: ${value}`);
  for(const id of ['energy','cost','unknown']) assert.equal(metricStatus(id,1000),null);
});

test('missing and invalid readings never receive a color',() => {
  for(const id of ['pm25','pm10','co2','hcho','tvoc','temp','humidity','power','speed','filter']) {
    for(const value of [null,undefined,NaN,Infinity,-Infinity,'12']) assert.equal(metricStatus(id,value),null,`${id}: ${value}`);
    if(id!=='temp') assert.equal(metricStatus(id,-1),null);
  }
  for(const id of ['humidity','speed','filter']) assert.equal(metricStatus(id,101),null);
});

test('device validity, independent sensors, offline state and filter alerts are respected',() => {
  const m1={model:'M1',online:true,state:{sensorValid:false,pm25:12,temp:24.5,co2:650}};
  assert.equal(deviceMetricStatus(m1),null);assert.equal(deviceMetricStatus(m1,'temp'),null);
  assert.equal(deviceMetricStatus(m1,'co2').tone,'good');
  const daikin={model:'DAIKIN',online:true,state:{sensorValid:false,pm25:null,pm10:18,co2:null,tvoc:0,hcho:null}};
  assert.equal(deviceMetricStatus(daikin,'pm10').tone,'good');assert.equal(deviceMetricStatus(daikin,'tvoc').tone,'good');
  for(const id of ['pm25','co2','hcho']) assert.equal(deviceMetricStatus(daikin,id),null);
  assert.equal(deviceMetricStatus({...daikin,online:false},'tvoc'),null);
  const a1={model:'A1',online:true,state:{on:false,appliedSpeed:90,filter:80,filterDue:true}};
  assert.equal(deviceMetricStatus(a1),null);assert.equal(deviceMetricStatus(a1,'filter').tone,'high');
  assert.equal(deviceMetricStatus({...a1,state:{...a1.state,on:true}}).tone,'high');
});
