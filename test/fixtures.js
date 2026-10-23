// Test-only implementations of the exact command envelopes used by the firmware.
import http from 'node:http';
import net from 'node:net';

export function firmware(model = 'TC1', mac = 'aabbcc000001', { unsupportedActions = [] } = {}) {
  const state = {
    model: model.toLowerCase(), device_id: mac, version: 'test-fixture', name: `测试 ${model}`,
    ...(model === 'TC1' ? { socket_states: [0, 0, 0, 0, 0, 0], socket_names: ['一', '二', '三', '四', '五', '六'], led_enabled: true, child_lock: false } : {}),
    ...(model === 'M1' ? { brightness_requested: 2, sensor_valid: true, temperature: 24.5, humidity: 51, pm25: 12, formaldehyde: 0.012, scd40: { valid: false, co2_ppm: null } } : {}),
    ...(model === 'A1' ? { fan_on: false, fan_mode: 'manual', fan_state: 'off', speed: 35, percentage: 0, applied_speed: 0, auto_max_speed: 80, fan_ready: true, led_enabled: true, filter: { remaining_percent: 92, replacement_due: false }, child_lock: false } : {})
  };
  const actions = ['status.get', 'capabilities.get', 'config.get', 'config.set', ...(model === 'TC1' ? ['outlet.set', 'indicator.set', 'power.get', 'child_lock.set'] : model === 'M1' ? ['display.set'] : ['fan.set', 'fan.mode.set', 'light.set', 'child_lock.set'])];
  const capabilities = { model: model.toLowerCase(), actions };
  const calls = [], power = { power_watts: 23.4, energy_kwh: 1.23 }; let config = {};
  function command(req) {
    calls.push(structuredClone(req)); const p = req.params || {}; let data = null, code = 'accepted';
    if (unsupportedActions.includes(req.action)) return { request_id: req.request_id, code: 'unsupported', message: 'unsupported', data: null };
    switch (req.action) {
      case 'info.get': data = { model: state.model, device_id: state.device_id, version: state.version }; code = 'ok'; break;
      case 'status.get': data = state; code = 'ok'; break;
      case 'capabilities.get': data = capabilities; code = 'ok'; break;
      case 'power.get': data = power; code = 'ok'; break;
      case 'config.get': data = config; code = 'ok'; break;
      case 'config.set': config = structuredClone(p); break;
      case 'outlet.set': if (p.index === undefined) state.socket_states.fill(p.on ? 1 : 0); else state.socket_states[p.index] = p.on ? 1 : 0; break;
      case 'display.set': state.brightness_requested = p.brightness / 25; break;
      case 'fan.mode.set': state.fan_mode = p.mode; break;
      case 'fan.set':
        if (p.mode !== undefined) state.fan_mode = p.mode;
        if (p.on !== undefined) state.fan_on = p.on;
        if (p.speed !== undefined) { if (state.fan_mode === 'auto') state.auto_max_speed = p.speed; else state.speed = p.speed; state.fan_on = p.speed > 0; }
        state.percentage = state.fan_on ? state.speed : 0; state.applied_speed = state.percentage; state.fan_state = state.fan_on ? 'running' : 'off'; break;
      case 'light.set': case 'indicator.set': state.led_enabled = p.enabled; break;
      case 'child_lock.set': state.child_lock = p.enabled; break;
      default: code = 'unsupported';
    }
    return JSON.parse(JSON.stringify({ request_id: req.request_id, code, message: code, data }));
  }
  return { state, capabilities, actions, power, calls, command, get config() { return config; } };
}
export async function httpDevice(model, mac, password = '', options = {}) {
  const f = firmware(model, mac, options); f.dropWrites = false; f.password = password;
  const server = http.createServer(async (req, res) => {
    if (f.password && req.headers.authorization !== `Basic ${Buffer.from(`admin:${f.password}`).toString('base64')}`) { res.writeHead(401); res.end('{}'); return; }
    const chunks = []; for await (const chunk of req) chunks.push(chunk);
    if (req.url !== '/api/v1/command' || req.method !== 'POST') { res.writeHead(404); res.end('{}'); return; }
    const request = JSON.parse(Buffer.concat(chunks)); const result = f.command(request);
    if (f.dropWrites && !request.action.endsWith('.get')) { req.socket.destroy(); return; }
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(result));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { ...f, get password() { return f.password; }, set password(p) { f.password = p; }, get dropWrites() { return f.dropWrites; }, set dropWrites(v) { f.dropWrites = v; }, address: `http://127.0.0.1:${server.address().port}`, async close() { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); } };
}
export async function freePort() {
  const server = net.createServer(); await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port; await new Promise(resolve => server.close(resolve)); return port;
}
export async function until(fn, timeout = 8000) {
  const deadline = Date.now() + timeout;
  while (!(await fn())) { if (Date.now() > deadline) throw new Error('condition timed out'); await new Promise(resolve => setTimeout(resolve, 25)); }
}
