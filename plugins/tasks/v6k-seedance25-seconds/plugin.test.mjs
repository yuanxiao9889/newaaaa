import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('./plugin.js', import.meta.url));
const plugin = await import('data:text/javascript;base64,' + source.toString('base64'));
const media = ['image', 'video', 'audio'].map(kind => ({
  type: kind + '_url', role: 'reference_' + kind, [kind + '_url']: { url: 'https://media.example/' + kind },
}));
for (const res of ['480', '720']) {
  test(res + 'p uses the channel mapping, bounded seconds and ordered client content', () => {
    const model = '全能视频2.5-301010-' + res + '-s';
    const raw = { model, prompt: 'Follow the references', ratio: '16:9', seconds: 10,
      omni_reference_task_type: 'reference', content: media };
    const decode = body => plugin.protocols.openai_video.decodeRequest({ operation: 'create', model, body: { kind: 'json', value: body } });
    const upstreamModel = 'channel-configured-' + res;
    const ctx = { model, upstreamModel, baseUrl: 'https://www.v6k.top', apiKey: 'fixture', requestBody: decode(raw).requestBody };
    assert.deepEqual(plugin.buildSubmitRequest(ctx).body, { model: upstreamModel, prompt: raw.prompt, seconds: 10,
      ratio: '16:9', resolution: res + 'p', images: ['https://media.example/image'], videos: ['https://media.example/video'], audios: ['https://media.example/audio'],
      omni_reference_task_type: 'reference' });
    const urls = { model, prompt: raw.prompt, seconds: 5, images: ['https://media.example/photo'], videos: ['https://media.example/clip'] };
    const descriptor = plugin.buildSubmitRequest({ ...ctx, requestBody: decode(urls).requestBody });
    assert.deepEqual(descriptor.body, { ...urls, model: upstreamModel, ratio: '16:9', resolution: res + 'p', omni_reference_task_type: 'reference' });
    const text = decode({ model, prompt: raw.prompt }).requestBody;
    assert.equal(plugin.buildSubmitRequest({ ...ctx, requestBody: text }).body.omni_reference_task_type, undefined);
    assert.deepEqual(plugin.extractUsage(ctx), { seconds: 10 });
    for (const patch of [{ seconds: 4 }, { seconds: 31 }, { duration: 8 }, { resolution: res === '480' ? '720p' : '480p' },
      { content: [{ ...media[0], role: 'first_frame' }] }, { content: Array(11).fill(media[1]) },
      { videos: ['https://media.example/conflict'] }]) assert.throws(() => decode({ ...raw, ...patch }));
  });
}

for (const key of ['v6k-seedance20', 'v6k-seedance25', 'v6k-seedance25-seconds']) {
  test(key + ' resolves GET redirects and supports direct video responses', async (t) => {
    const js = await readFile(new URL('../' + key + '/plugin.js', import.meta.url));
    const driver = await import('data:text/javascript;base64,' + js.toString('base64'));
    const canonical = 'https://www.v6k.top/v1/videos/task_1/content';
    const signed = 'https://assets.example/archive/clip.video?signature=test';
    const ctx = { upstreamTaskId: 'task_1', artifactKey: 'video', apiKey: 'fixture', baseUrl: 'https://www.v6k.top' };
    const headers = { Authorization: 'Bearer fixture' };
    let response = { status: 303, headers: { Location: signed } };
    const calls = [];
    globalThis.utils = { fetch(request) {
      calls.push(request);
      if (request.method === 'HEAD') return { status: 200, headers: {} };
      if (response instanceof Error) throw response;
      return response;
    } };
    t.after(() => { delete globalThis.utils; });
    const content = method => driver.buildContentRequest({ ...ctx, clientRequest: { method } });
    const probe = { url: canonical, method: 'GET', headers: { ...headers, Range: 'bytes=0-0' }, responseType: 'bytes' };
    for (const method of ['GET', 'HEAD']) {
      assert.deepEqual(content(method), { method: 'GET', url: signed, credentialless: true });
      assert.deepEqual(calls.splice(0), [probe]);
    }
    response = { status: 303, headers: { Location: 'http://assets.example/video.mp4' } };
    assert.throws(() => content('GET'), /Invalid upstream video download URL/);
    calls.length = 0;
    response = { status: 206, headers: {} };
    assert.deepEqual(content('GET'), { method: 'GET', url: canonical, headers });
    assert.deepEqual(calls.splice(0), [probe]);
    response = new Error('range ignored, response too large');
    assert.deepEqual(content('HEAD'), { method: 'HEAD', url: canonical, headers });
    assert.deepEqual(calls, [probe, { url: canonical, method: 'HEAD', headers }]);
  });
}
