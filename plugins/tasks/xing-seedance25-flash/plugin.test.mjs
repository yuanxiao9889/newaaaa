import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const load = async path => import('data:text/javascript;base64,' + Buffer.from(await readFile(new URL(path, import.meta.url))).toString('base64'));
const xing = await load('./plugin.js');
const model = '全能视频2.5-1055-flash';
const decode = (plugin, fields = {}) => plugin.protocols.openai_video.decodeRequest({ operation: 'create', model, body: { kind: 'json', value: { model, prompt: 'fixture', ...fields } } });
const context = fields => ({ model, upstreamModel: 'seedance2.5-flash-M', baseUrl: 'https://xingapi.top', apiKey: 'fixture-key', requestBody: decode(xing, fields).requestBody });

test('Flash forwards mixed references and exposes count, seconds and resolution for pricing', () => {
  const fields = { seconds: '30', resolution: '720p', metadata: { ratio: '9:16', images: ['https://media.example/i.jpg'], reference_videos: ['https://media.example/v.mp4'], reference_audios: ['https://media.example/a.mp3'] } };
  const ctx = context(fields), request = xing.buildSubmitRequest(ctx);
  assert.equal(request.url, 'https://xingapi.top/v1/videos/generations');
  assert.deepEqual(request.body, { model: ctx.upstreamModel, prompt: 'fixture', duration: 30, ratio: '9:16', resolution: '720p', images: fields.metadata.images, videos: fields.metadata.reference_videos, audios: fields.metadata.reference_audios });
  assert.deepEqual(xing.extractUsage(ctx), {video_count: 1, seconds: 30, resolution: '720p'});
  assert.equal(ctx.requestBody.model, model);
});

test('editable upstream mapping has no provider ID whitelist', () => {
  for (const id of ['seedance2.5-flash-M', 'seedance2.5-V', 'future-compatible-id']) {
    assert.equal(xing.buildSubmitRequest({ ...context({}), upstreamModel: id }).body.model, id);
  }
  for (const id of [undefined, '', ' ', model, ' bad', 'bad\nmodel']) assert.throws(() => xing.buildSubmitRequest({ ...context({}), upstreamModel: id }));
});

test('provider reference limits and duration bounds apply before billing', () => {
  const maximum = { duration: 30, images: Array(10).fill('https://media.example/i.jpg'), videos: Array(5).fill('https://media.example/v.mp4'), audios: Array(5).fill('https://media.example/a.mp3') };
  assert.equal(xing.buildSubmitRequest(context(maximum)).body.audios.length, 5);
  assert.equal(xing.extractUsage(context({ duration: 4 })).seconds, 4);
  for (const fields of [{ seconds: 3 }, { duration: 31 }, { duration: 18446744073686646784 }, { seconds: 5, duration: 6 }, { resolution: '1080p' }, { n: 2 }, { stream: true }, { metadata: { duration: -1 } }, { ratio: '9:16', aspect_ratio: '16:9' }, { images: Array(11).fill('https://media.example/a') }, { videos: Array(6).fill('https://media.example/a') }, { audios: Array(6).fill('https://media.example/a') }, { image: 'http://127.0.0.1/a' }]) assert.throws(() => decode(xing, fields));
});

test('documented envelopes and task IDs survive submission and polling', () => {
  for (const field of ['id', 'task_id', 'request_id']) {
    const body = { [field]: 'videoprep_fixture', status: 'preparing' };
    for (const envelope of [body, { data: body }]) {
      const submitted = xing.parseSubmitResponse(context({}), { statusCode: 202, body: envelope });
      assert.equal(submitted.taskId, 'videoprep_fixture');
      assert.equal(submitted.immediate, undefined);
      assert.equal(xing.parseTaskResult({ taskId: submitted.taskId }, envelope).status, 'QUEUED');
    }
  }
  assert.throws(() => xing.parseSubmitResponse(context({}), { statusCode: 202, body: { id: 'one', request_id: 'two' } }), /Conflicting/);
  for (const [state, expected] of [['processing', 'IN_PROGRESS'], ['completed', 'SUCCESS'], ['expired', 'FAILURE'], ['constructor', 'UNKNOWN'], ['unrecognized', 'UNKNOWN']]) assert.equal(xing.parseTaskResult({ taskId: 'task' }, { id: 'task', status: state }).status, expected);
  assert.equal(xing.parseTaskResult({ taskId: 'task' }, { id: 'other', status: 'completed' }).status, 'UNKNOWN');
  assert.equal(xing.parseTaskResult({ taskId: 'task' }, { id: 'task', status: 'completed' }, { status: 503 }).status, 'UNKNOWN');
});

test('content downloads preserve original task ID and never send credentials to storage', () => {
  const ctx = { ...context({}), upstreamTaskId: 'videoprep_fixture', artifactKey: 'video', clientRequest: { method: 'GET' } };
  assert.deepEqual(xing.buildContentRequest(ctx), { url: 'https://xingapi.top/v1/videos/videoprep_fixture/content', method: 'GET', headers: { Authorization: 'Bearer fixture-key' } });
  assert.deepEqual(xing.buildContentRequest({ ...ctx, data: { data: { download_url: 'https://storage.example/result.mp4' } } }), { url: 'https://storage.example/result.mp4', method: 'GET', credentialless: true });
  assert.equal(xing.buildContentRequest({ ...ctx, data: { url: 'https://storage.example/result.mp4?key=fixture-key' } }).url, 'https://xingapi.top/v1/videos/videoprep_fixture/content');
});

test('downloads refresh terminal snapshots and resolve redirects without forwarding credentials', () => {
  const canonical = 'https://xingapi.top/v1/videos/videoprep_fixture/content';
  const direct = 'https://space-video.coze.site/result.mp4?auth_key=fresh';
  const ctx = { ...context({}), upstreamTaskId: 'videoprep_fixture', artifactKey: 'video', clientRequest: { method: 'HEAD' }, data: { id: 'videoprep_fixture', status: 'completed', url: canonical } };
  const previousUtils = globalThis.utils;
  try {
    for (const refresh of [true, false]) {
      const requests = [];
      globalThis.utils = { fetch(request) {
        requests.push(request);
        assert.equal(request.headers.Authorization, 'Bearer fixture-key');
        assert.equal(request.method, 'GET');
        if (request.url === canonical) return { status: 302, headers: { Location: direct } };
        assert.equal(request.url, canonical.replace('/content', ''));
        return { status: 200, body: { data: { id: ctx.upstreamTaskId, status: 'completed', url: canonical, download_url: refresh ? direct : canonical } } };
      } };
      assert.deepEqual(xing.buildContentRequest(ctx), { url: direct, method: 'GET', credentialless: true });
      assert.equal(requests.length, refresh ? 1 : 2);
      assert.equal(ctx.data.url, canonical);
    }
    globalThis.utils = { fetch() { throw new Error('temporary upstream outage'); } };
    assert.deepEqual(xing.buildContentRequest({ ...ctx, data: { url: direct } }), { url: direct, method: 'GET', credentialless: true });
    assert.deepEqual(xing.buildContentRequest(ctx), { url: canonical, method: 'GET', headers: { Authorization: 'Bearer fixture-key' } });
    for (const location of ['http://storage.example/file.mp4', 'https://127.0.0.1/file', 'https://storage.example/file?key=fixture-key']) {
      globalThis.utils = { fetch(request) {
        if (request.url === canonical) return { status: 302, headers: { Location: location } };
        return { status: 200, body: { id: 'another-task', status: 'completed', download_url: direct } };
      } };
      assert.equal(xing.buildContentRequest(ctx).url, canonical);
    }
  } finally {
    if (previousUtils === undefined) delete globalThis.utils;
    else globalThis.utils = previousUtils;
  }
});


test('both resolution tiers, optional false and zero, and channel base paths are preserved', () => {
  for (const resolution of ['480p', '720p']) {
    const ctx = context({resolution, generate_audio: false, seed: 0});
    const req = xing.buildSubmitRequest({...ctx, baseUrl: 'https://xingapi.top/v1/'});
    assert.equal(req.url, 'https://xingapi.top/v1/videos/generations');
    assert.equal(req.body.resolution, resolution);
    assert.equal(req.body.generate_audio, false);
    assert.equal(req.body.seed, 0);
    assert.deepEqual(xing.extractUsage(ctx), {video_count: 1, seconds: 5, resolution});
  }
  const body = xing.buildSubmitRequest(context({})).body;
  assert.equal(Object.hasOwn(body, 'generate_audio'), false);
  assert.equal(Object.hasOwn(body, 'seed'), false);
  for (const fields of [{generate_audio: 1}, {seed: 0.5}, {seed: 9007199254740992}]) assert.throws(() => decode(xing, fields));
});
