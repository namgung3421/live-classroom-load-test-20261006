// Generate synthetic VP8 IVF layers with the runner's existing Chrome/Node.
// Uses only local canvas pixels. Never opens a camera or captures a desktop.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const { spawn } = require('node:child_process');
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function main() {
  const chrome = process.env.CHROME_BIN || 'google-chrome';
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'live-class-video-'));
  const folder = path.resolve(__dirname, 'video');
  fs.mkdirSync(folder, { recursive: true });
  const server = http.createServer((_, response) => { response.setHeader('Content-Type', 'text/html'); response.end('<!doctype html><title>Synthetic video fixture</title>'); });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}/`;
  const chromeProcess = spawn(chrome, ['--headless=new', '--no-sandbox', '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-dev-shm-usage', '--disable-gpu', '--remote-debugging-port=0', `--user-data-dir=${profile}`, url], { windowsHide: true, stdio: ['ignore','ignore','pipe'] });
  let chromeDiagnostics='';
  chromeProcess.stderr.on('data',data=>{chromeDiagnostics=(chromeDiagnostics+data.toString()).slice(-2000);});
  let socket;
  try {
    const activePort = path.join(profile, 'DevToolsActivePort');
    const deadline = Date.now() + 60000;
    while (!fs.existsSync(activePort)) { assert.ok(Date.now() < deadline && chromeProcess.exitCode === null, 'Fixture Chrome did not start: '+chromeDiagnostics); await delay(100); }
    const port = Number(fs.readFileSync(activePort, 'utf8').split(/\r?\n/)[0]);
    const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
    const tab = targets.find(value => value.type === 'page' && value.url === url);
    assert.ok(tab, 'The owned fixture page was not found.');
    socket = new WebSocket(tab.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
    let id = 0;
    const pending = new Map();
    socket.addEventListener('message', message => {
      const value = JSON.parse(message.data);
      if (!value.id || !pending.has(value.id)) return;
      const request = pending.get(value.id); pending.delete(value.id);
      if (value.error) request.reject(new Error(value.error.message)); else request.resolve(value.result);
    });
    const call = (method, params) => new Promise((resolve, reject) => { const requestId = ++id; pending.set(requestId, { resolve, reject }); socket.send(JSON.stringify({ id: requestId, method, params })); });
    const readyDeadline = Date.now() + 20000;
    while (true) {
      const ready = await call('Runtime.evaluate', { expression: '({ href: location.href, ready: document.readyState, secure: isSecureContext, encoder: typeof VideoEncoder })', returnByValue: true });
      const state = ready.result.value;
      if (state.href === url && state.ready === 'complete') { assert.ok(state.secure && state.encoder === 'function', JSON.stringify(state)); break; }
      assert.ok(Date.now() < readyDeadline, 'The synthetic page did not finish loading.'); await delay(100);
    }
    for (const [name, width, height, bitrate] of [['low',320,180,150000], ['medium',640,360,600000], ['high',1920,1080,3000000]]) {
      const expression = `(${async function encode(width, height, bitrate) {
        if (!window.isSecureContext || typeof VideoEncoder !== 'function') throw new Error('WebCodecs is unavailable.');
        const support = await VideoEncoder.isConfigSupported({ codec: 'vp8', width, height, bitrate, framerate: 20, latencyMode: 'realtime' });
        if (!support.supported) throw new Error('VP8 encoding is unavailable.');
        const canvas = document.createElement('canvas'); canvas.width = width; canvas.height = height;
        const context = canvas.getContext('2d');
        const chunks = []; let failure;
        const encoder = new VideoEncoder({ output(chunk) { const data = new Uint8Array(chunk.byteLength); chunk.copyTo(data); let value = ''; for (let i = 0; i < data.length; i += 8192) value += String.fromCharCode(...data.subarray(i, i + 8192)); chunks.push({ timestamp: chunk.timestamp, key: chunk.type === 'key', data: btoa(value) }); }, error(error) { failure = error; } });
        encoder.configure(support.config);
        for (let frame = 0; frame < 100; frame++) {
          for (let y = 0; y < 18; y++) for (let x = 0; x < 32; x++) {
            context.fillStyle = `hsl(${(x * 37 + y * 79 + frame * 9) % 360} 65% ${(x + y + frame) % 2 ? 40 : 65}%)`;
            context.fillRect(Math.floor(x * width / 32), Math.floor(y * height / 18), Math.ceil(width / 32), Math.ceil(height / 18));
          }
          context.fillStyle = '#fff'; context.font = `${Math.max(12, height / 16)}px sans-serif`; context.fillText(`Synthetic student ${frame}`, width / 20, height / 8);
          const videoFrame = new VideoFrame(canvas, { timestamp: frame * 50000, duration: 50000 });
          encoder.encode(videoFrame, { keyFrame: frame % 20 === 0 }); videoFrame.close();
          while (encoder.encodeQueueSize > 4 && !failure) await new Promise(resolve => setTimeout(resolve, 5));
          if (failure) throw failure;
        }
        await encoder.flush(); encoder.close();
        return chunks;
      }.toString()})(${width},${height},${bitrate})`;
      const result = await call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      assert.ok(!result.exceptionDetails, result.exceptionDetails?.exception?.description || 'Synthetic video encoding failed.');
      const chunks = result.result.value;
      assert.ok(chunks.length === 100 && chunks[0].key);
      const header = Buffer.alloc(32); header.write('DKIF', 0); header.writeUInt16LE(32, 6); header.write('VP80', 8);
      header.writeUInt16LE(width, 12); header.writeUInt16LE(height, 14); header.writeUInt32LE(20, 16); header.writeUInt32LE(1, 20); header.writeUInt32LE(chunks.length, 24);
      const pieces = [header];
      for (const [i, chunk] of chunks.entries()) { const bytes = Buffer.from(chunk.data, 'base64'); const entry = Buffer.alloc(12); entry.writeUInt32LE(bytes.length, 0); entry.writeBigUInt64LE(BigInt(i), 4); pieces.push(entry, bytes); }
      const bytes = Buffer.concat(pieces); fs.writeFileSync(path.join(folder, `${name}.ivf`), bytes);
      console.log(JSON.stringify({ layer: name, width, height, fps: 20, frames: chunks.length, bytes: bytes.length }));
    }
    const audioResult = await call('Runtime.evaluate', { awaitPromise: true, returnByValue: true, expression: `(${async function encodeAudio() {
      const support = await AudioEncoder.isConfigSupported({ codec: 'opus', sampleRate: 48000, numberOfChannels: 1, bitrate: 32000 });
      if (!support.supported) throw new Error('Opus encoding is unavailable.');
      const chunks = []; let failure;
      const encoder = new AudioEncoder({ output(chunk) { const bytes = new Uint8Array(chunk.byteLength); chunk.copyTo(bytes); chunks.push(btoa(String.fromCharCode(...bytes))); }, error(error) { failure = error; } });
      encoder.configure(support.config);
      for (let frame = 0; frame < 250; frame++) {
        const samples = new Float32Array(960);
        for (let i = 0; i < samples.length; i++) { const time = (frame * 960 + i) / 48000; samples[i] = .15 * Math.sin(time * 2 * Math.PI * 440) + .1 * Math.sin(time * 2 * Math.PI * 660); }
        const audio = new AudioData({ format: 'f32-planar', sampleRate: 48000, numberOfFrames: 960, numberOfChannels: 1, timestamp: frame * 20000, data: samples });
        encoder.encode(audio); audio.close();
        while (encoder.encodeQueueSize > 5 && !failure) await new Promise(resolve => setTimeout(resolve, 5));
        if (failure) throw failure;
      }
      await encoder.flush(); encoder.close(); return chunks;
    }.toString()})()` });
    assert.ok(!audioResult.exceptionDetails, audioResult.exceptionDetails?.exception?.description || 'Synthetic audio encoding failed.');
    const audioChunks = audioResult.result.value; assert.ok(audioChunks.length >= 250);
    fs.writeFileSync(path.join(folder, 'audio.json'), JSON.stringify(audioChunks));
    console.log(JSON.stringify({ audio: 'opus', sampleRate: 48000, packets: audioChunks.length, bytes: audioChunks.reduce((sum, value) => sum + Buffer.from(value, 'base64').length, 0) }));
    await call('Browser.close', {}).catch(() => {});
  } finally {
    socket?.close();
    if (chromeProcess.exitCode === null) chromeProcess.kill('SIGTERM');
    await new Promise(resolve => server.close(resolve));
    // This generated profile is exclusively owned by this process.
    assert.ok(path.dirname(profile) === os.tmpdir() && path.basename(profile).startsWith('live-class-video-'));
    await delay(300);
    fs.rmSync(profile, { recursive: true, force: true, maxRetries: 4, retryDelay: 200 });
  }
}
main().catch(error => { console.error(error.message); process.exitCode = 1; });
