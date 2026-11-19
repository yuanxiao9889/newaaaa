import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const plugin = await import('data:text/javascript;base64,' + Buffer.from(await readFile(new URL('./plugin.js', import.meta.url))).toString('base64'));
const context = (model, fields = {}) => ({ model, upstreamModel: model, baseUrl: 'https://www.newtoken.club', apiKey: 'fixture-key',
  requestBody: { model, prompt: 'Animate @image1', seconds: 4, ...(model.endsWith('token') ? { videos: ['https://media.example/reference.mp4'] } : {}), ...fields } });

test('all public aliases produce native requests with explicit 720p and unchanged billing meters', () => {
  for (const model of plugin.meta.models) {
    const ctx = context(model, { images: ['https://media.example/image.jpg'], generate_audio: false, seed: 0 });
    const intent = plugin.protocols.openai_video.decodeRequest({ operation: 'create', model, body: { kind: 'json', value: ctx.requestBody } });
    assert.deepEqual(intent.requestBody, ctx.requestBody);
    const req = plugin.buildSubmitRequest(ctx);
    assert.equal(req.url, 'https://www.newtoken.club/api/v3/contents/generations/tasks');
    assert.equal(req.body.model, model.includes('2.5') ? 'doubao-seedance-2-5-260628' : 'doubao-seedance-2-0-260128');
    assert.equal(req.body.resolution, '720p');
    assert.equal(req.body.duration, 4);
    assert.equal(req.body.ratio, '16:9');
    assert.equal(req.body.generate_audio, false);
    assert.equal(req.body.seed, 0);
    assert.equal(req.body.content[0].text, 'Animate @Image1');
    assert.deepEqual(req.body.content[1], { type: 'image_url', role: 'reference_image', image_url: { url: 'https://media.example/image.jpg' } });
    assert.equal(req.body.omni_reference_task_type, model.includes('2.5') && model.endsWith('token') ? 'reference' : undefined);
    assert.deepEqual(plugin.extractUsage(ctx), model.endsWith('token') ? { tokens: 129600 } : { seconds: 4, video_input: 'none' });
    assert.equal(plugin.buildSubmitRequest({ ...ctx, baseUrl: ctx.baseUrl + '/v1/', upstreamModel: 'custom-native-endpoint' }).body.model, 'custom-native-endpoint');
    assert.equal(plugin.buildSubmitRequest({ ...ctx, baseUrl: ctx.baseUrl + '/v1/' }).url, req.url);
  }
});

test('reference roles, ratios and default resolution survive the conversion', () => {
  const model = plugin.meta.models[0];
  const content = [
    { type: 'image_url', role: 'first_frame', image_url: { url: 'https://media.example/first.jpg' } },
    { type: 'image_url', role: 'last_frame', image_url: { url: 'https://media.example/last.jpg' } },
  ];
  const req = plugin.buildSubmitRequest(context(model, { content, aspect_ratio: '9:16', seconds: undefined, duration: '15s' }));
  assert.deepEqual(req.body.content.slice(1), content);
  assert.equal(req.body.ratio, '9:16');
  assert.equal(req.body.duration, 15);
  assert.equal(req.body.resolution, '720p');
});

test('invalid quantities, resolution, references and conflicting aliases fail before submission', () => {
  for (const model of plugin.meta.models) {
    for (const fields of [{ seconds: -1 }, { seconds: 18446744073686646784 }, { seconds: 3 }, { seconds: model.includes('2.5') ? 31 : 16 },
      { resolution: '480p' }, { resolution: '1080p' }, { seconds: 4, duration: 5 }, { ratio: '16:9', aspect_ratio: '9:16' },
      { n: 2 }, { generate_audio: 0 }, { seed: 1.5 }, { images: ['http://localhost/image.jpg'] }, { stream: true }]) {
      assert.throws(() => plugin.buildSubmitRequest(context(model, fields)), JSON.stringify(fields));
      assert.throws(() => plugin.extractUsage(context(model, fields)), JSON.stringify(fields));
    }
    assert.throws(() => plugin.buildSubmitRequest(context(model, { videos: model.endsWith('token') ? [] : ['https://media.example/v.mp4'] })), /reference video/i);
  }
});

test('new tasks persist official protocol while pre-upgrade tasks keep their old query endpoint', () => {
  const ctx = context(plugin.meta.models[0]);
  const parsed = plugin.parseSubmitResponse(ctx, { statusCode: 200, body: { id: 'ultra720_fixture' } });
  assert.equal(parsed.taskId, 'ultra720_fixture');
  assert.equal(parsed.state.protocol, 'official');
  const queryCtx = { ...ctx, taskId: parsed.taskId, state: parsed.state };
  assert.equal(plugin.buildQueryRequest(queryCtx).url, ctx.baseUrl + '/api/v3/contents/generations/tasks/ultra720_fixture');
  assert.equal(plugin.buildQueryRequest({ ...queryCtx, state: { seconds: 4 } }).url, ctx.baseUrl + '/v1/videos/ultra720_fixture');
  assert.equal(plugin.buildQueryRequest({ ...queryCtx, state: undefined }).url, ctx.baseUrl + '/v1/videos/ultra720_fixture');
});

test('native success, failure, unknown and legacy results preserve task contracts', () => {
  const ctx = { ...context(plugin.meta.models[0]), taskId: 'fixture' };
  const native = { id: 'fixture', status: 'succeeded', duration: 4, resolution: '720p', content: { video_url: 'https://media.example/result.mp4' }, usage: { completion_tokens: 108000, total_tokens: 110000 } };
  assert.deepEqual(plugin.parseTaskResult(ctx, native), { status: 'SUCCESS', progress: '100%', tokenUsage: { completion_tokens: 108000, total_tokens: 110000 } });
  assert.equal(plugin.parseTaskResult(ctx, { id: 'fixture', status: 'processing' }).status, 'IN_PROGRESS');
  assert.equal(plugin.parseTaskResult(ctx, { ...native, id: 'other' }).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult(ctx, { ...native, status: 'new-state' }).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult(ctx, native, { status: 503 }).status, 'UNKNOWN');
  const failure = plugin.parseTaskResult(ctx, { id: 'fixture', status: 'failed', error: { message: 'rejected fixture-key https://upstream.example/private' } });
  assert.equal(failure.status, 'FAILURE');
  assert.ok(!failure.reason.includes(ctx.apiKey));
  assert.ok(!failure.reason.includes('https://'));
  assert.equal(plugin.parseTaskResult(ctx, { id: 'fixture', status: 'completed', video_url: native.content.video_url }).status, 'SUCCESS');
  assert.equal(plugin.parseTaskResult(ctx, { ...native, content: {} }).status, 'IN_PROGRESS');
});

test('token settlement uses actual total tokens including explicit zero and rejects absent or invalid facts', () => {
  const ctx = { ...context(plugin.meta.models.find(m => m.endsWith('token'))), taskId: 'fixture' };
  for (const total of [0, 216899]) {
    const body = { id: 'fixture', status: 'succeeded', content: { video_url: 'https://media.example/v.mp4' }, usage: { total_tokens: total } };
    const result = plugin.parseTaskResult(ctx, body);
    assert.equal(result.status, 'SUCCESS');
    assert.deepEqual(plugin.extractUsageOnComplete(ctx, result, body), { tokens: total });
  }
  for (const total of [undefined, -1, 0.5, 2147483648, '216899']) {
    assert.equal(plugin.parseTaskResult(ctx, { id: 'fixture', status: 'succeeded', content: { video_url: 'https://media.example/v.mp4' }, usage: { total_tokens: total } }).status, 'UNKNOWN');
  }
  assert.deepEqual(plugin.extractUsageOnComplete(ctx, { status: 'FAILURE' }, {}), {});
});

test('second-priced settlement stays separate from upstream token accounting', () => {
  const ctx = context(plugin.meta.models[0]);
  assert.deepEqual(plugin.extractUsageOnComplete(ctx, { status: 'SUCCESS' }, { duration: 6, usage: { total_tokens: 123456 } }), { seconds: 6 });
  assert.deepEqual(plugin.extractUsageOnComplete(ctx, { status: 'SUCCESS' }, {}), {});
  assert.throws(() => plugin.extractUsageOnComplete(ctx, { status: 'SUCCESS' }, { duration: 1e30 }));
});

test('native and legacy artifacts download without forwarding channel credentials', () => {
  for (const data of [{ content: { video_url: 'https://media.example/v.mp4' } }, { video_url: 'https://media.example/v.mp4' }]) {
    assert.deepEqual(plugin.buildContentRequest({ ...context(plugin.meta.models[0]), artifactKey: 'video', clientRequest: { method: 'GET' }, data }), { url: 'https://media.example/v.mp4', method: 'GET', credentialless: true });
  }
  assert.throws(() => plugin.buildContentRequest({ ...context(plugin.meta.models[0]), artifactKey: 'video', clientRequest: { method: 'GET' }, data: { content: { video_url: 'https://media.example/v.mp4?key=fixture-key' } } }));
  const rendered = plugin.protocols.openai_video.render({}, { status: 'SUCCESS', data: { duration: 4, ratio: '9:16', usage: { total_tokens: 108000 } } });
  assert.deepEqual(rendered, { resolution: '720p', seconds: '4', aspect_ratio: '9:16', usage: { total_tokens: 108000 } });
});
