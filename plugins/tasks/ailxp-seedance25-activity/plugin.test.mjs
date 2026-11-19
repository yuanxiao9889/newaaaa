import assert from 'node:assert/strict';
import * as p from './plugin.js';
const model = p.meta.models[0];
assert.equal(model, '全能视频2.5-30-720');
assert.equal(p.meta.usageSchema.seconds.description.zh, '视频生成单价');
const ctx = { model, upstreamModel: 'future-upstream-A', baseUrl: 'https://ailxp.top', apiKey: 'fixture-key', requestBody: { model, prompt: 'test' } };
const request = fields => p.buildSubmitRequest({ ...ctx, requestBody: { ...ctx.requestBody, ...fields } });
for (let duration = 4; duration <= 30; duration++) {
  const r = request({ duration });
  assert.equal(r.body.seconds, duration);
  assert.equal(r.body.model, 'future-upstream-A');
  assert.equal(r.url, 'https://ailxp.top/v1/videos');
  assert.deepEqual(p.extractUsage({ ...ctx, requestBody: { ...ctx.requestBody, duration } }), { seconds: duration });
}
for (const duration of [0, 1, 2, 3, 31, 4.5, null, true, '5s']) assert.throws(() => request({ duration }));
assert.throws(() => request({ seconds: 4, duration: 15 }));
assert.equal(request({ seconds: '30' }).body.seconds, 30);
assert.equal(request({}).body.seconds, 5);
assert.equal(request({resolution:'720p'}).body.resolution, undefined);
for (const ratio of ['16:9', '9:16', '1:1', '4:3', '3:4', '21:9']) assert.equal(request({ ratio }).body.ratio, ratio);
for (const [kind, count] of [['images', 30], ['videos', 10], ['audios', 10]]) {
  const refs = Array.from({ length: count }, (_, n) => 'https://media.example/' + kind + '/' + n);
  assert.deepEqual(request({ [kind]: refs }).body.files, refs.map(url => ({url})));
  assert.throws(() => request({ [kind]: [...refs, refs[0]] }));
}
assert.deepEqual(request({ image_url: 'https://media.example/a.jpg' }).body.files, [{url:'https://media.example/a.jpg'}]);
assert.throws(() => request({images:['data:image/png;base64,YWJj']}));
assert.throws(() => request({ images: ['data:audio/mp3;base64,YWJj'] }));
for (const url of ['file:///a.jpg', 'http://localhost/a', 'http://127.0.0.1/a']) assert.throws(() => request({ images: [url] }));
assert.throws(() => request({ images: ['https://media.example/a'], image_url: 'https://media.example/b' }));
assert.throws(() => request({ tokens: 1 }));
for (const upstreamModel of ['future-upstream-A', 'future-upstream-B']) assert.equal(p.buildSubmitRequest({ ...ctx, upstreamModel }).body.model, upstreamModel);
assert.equal(p.buildSubmitRequest({ ...ctx, upstreamModel: undefined }).body.model, model);
const decoded = p.protocols.openai_video.decodeRequest({ model, operation: 'create', body: { kind: 'json', value: { model, prompt: 'test', duration: 30, ratio: '9:16' } } });
assert.equal(p.buildSubmitRequest({ ...ctx, requestBody: decoded.requestBody }).body.seconds, 30);
assert.equal(p.parseSubmitResponse(ctx, { statusCode: 200, body: { id: 'task_1', status: 'queued' } }).taskId, 'task_1');
assert.throws(() => p.parseSubmitResponse(ctx, { statusCode: 200, body: { id: 'task_1', task_id: 'task_2' } }));
for (const [status, expected] of [['NOT_START', 'QUEUED'], ['IN_PROGRESS', 'IN_PROGRESS'], ['SUCCESS', 'SUCCESS'], ['FAILURE', 'FAILURE'], ['alien', 'UNKNOWN']]) assert.equal(p.parseTaskResult({ ...ctx, taskId: 'task_1' }, { id: 'task_1', status }).status, expected);
assert.equal(p.parseTaskResult({ ...ctx, taskId: 'task_2' }, { id: 'task_1', status: 'SUCCESS' }).status, 'UNKNOWN');
assert.equal(p.parseTaskResult({ ...ctx, taskId: 'task_1' }, { id: 'task_1', status: 'SUCCESS' }, { status: 500 }).status, 'UNKNOWN');
assert.equal(p.buildContentRequest({ ...ctx, upstreamTaskId: 'task_1', artifactKey: 'video', clientRequest: { method: 'GET' } }).url, 'https://ailxp.top/v1/videos/task_1/content');
assert.equal(p.parseTaskResult({ ...ctx, taskId: 'task_1' }, { id: 'task_1', status: 'FAILURE', fail_reason: 'bad fixture-key' }).reason, 'bad [redacted]');
console.log('Passed Seedance 2.5 parameters, 30/10/10 references, aliases, dynamic mapping, protocol and task lifecycle.');

const refs = n => Array.from({length:n}, (_,i) => `https://media.example/${i}`);
assert.doesNotThrow(()=>request({images:refs(10),videos:refs(10),audios:refs(10)}));
assert.throws(()=>request({images:refs(11),videos:refs(10),audios:refs(10)}),/30 references in total/);
for (const field of ['videos','audios']) assert.throws(()=>request({[field]:refs(11)}));
for (const patch of [{images:refs(10)},{videos:refs(1)},{audios:refs(1)}]) {
 assert.throws(()=>request({seconds:4,...patch}),/at least 5/);
 assert.doesNotThrow(()=>request({seconds:5,...patch}));
}
assert.doesNotThrow(()=>request({seconds:4,images:refs(9)}));
assert.deepEqual(request({videos:refs(1),audios:refs(1)}).body.files, [...refs(1),...refs(1)].map(url=>({url})));
assert.equal(request({ratio:'auto'}).body.ratio,'auto');
assert.deepEqual(request({metadata:{ratio:'9:16',reference_videos:refs(1)}}).body,
  {model:ctx.upstreamModel,prompt:'test',seconds:5,ratio:'9:16',files:refs(1).map(url=>({url}))});
assert.throws(()=>request({ratio:'16:9',metadata:{ratio:'9:16'}}));
assert.throws(()=>request({resolution:'1080p'}));
const mixed = {images:['https://media.example/i.png'],videos:['https://media.example/v.mp4?signature=fixture'],audios:['https://media.example/a.mp3']};
const wire = request(mixed);
assert.deepEqual(wire.body, {model:ctx.upstreamModel,prompt:'test',seconds:5,ratio:'16:9',files:[...mixed.images,...mixed.videos,...mixed.audios].map(url=>({url}))});
assert.equal(wire.headers['Content-Type'],'application/json');
assert.deepEqual(request({}).body,{model:ctx.upstreamModel,prompt:'test',seconds:5,ratio:'16:9'});
const normalized = p.protocols.openai_video.decodeRequest({model,operation:'create',body:{kind:'json',value:{...ctx.requestBody,...mixed}}});
assert.deepEqual(p.buildSubmitRequest({...ctx,requestBody:normalized.requestBody}),wire);
assert.throws(()=>request({prompt:'a'.repeat(45001)}),/45000/);
console.log('AILXP contract: top-level ratio/files, no metadata/resolution/size, preserved URL order and decoder round trip passed.');
