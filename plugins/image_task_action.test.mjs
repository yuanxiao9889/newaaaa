import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const providers = [
  ['mai-seedream55', 'dou-5-5', 'seedream-5.5'],
  ['aicost-seedream55', 'dou-5-5', 'seedream-5.5'],
  ['pidoi-seedream55', 'dou-5-5', 'seedream_5.5'],
  ['duola-seedream55', 'dou-5-5', 'doubao-seedream-5-5-261028'],
  ['kuaiai-image25', 'all-image-2.5-sunburst-H'],
  ['sunburst-images', 'all-image-2.6-sunburst-H', 'gpt-image-2.6-sunburst'],
  ['jieapi-flux3', 'flux-3-image'],
  ['tencent-hy-image', 'hy-image-v3.5-preview'],
  ['doubao', 'doubao-seedream-5-0-lite-260128'],
  ['alibaba', 'wan2.7-image'],
];
for (const [key, model, upstreamModel = model] of providers) {
  const source = await readFile(new URL(`./tasks/${key}/plugin.js`, import.meta.url), 'utf8');
  const plugin = await import('data:text/javascript;base64,' + Buffer.from(source + `\n//# sourceURL=${key}-plugin.js`).toString('base64'));
  test(`${key}: image task actions remain queryable for generation and file editing`, () => {
    for (const operation of ['generate', 'edit']) {
      const body = operation === 'generate' ? { kind: 'json', value: { prompt: 'draw a cat' } } : {
        kind: 'multipart', fields: { prompt: ['edit the cat'] },
        files: [{ field: 'image', ref: 'request_file:image', filename: 'reference.png', mimeType: 'image/png', size: 32 }],
      };
      if (operation === 'edit' && ['mai-seedream55', 'pidoi-seedream55', 'aicost-seedream55'].includes(key)) {
        body.fields.oopii_reference_urls_v1 = ['["https://reference.example/image.png"]'];
      }
      const intent = plugin.protocols.openai_image.decodeRequest({ model, operation, body });
      assert.equal(intent.action, operation === 'edit' ? 'image_edit' : 'image_generate');
      const ctx = { model, upstreamModel, action: intent.action, requestBody: intent.requestBody,
        baseUrl: 'https://upstream.example', apiKey: 'fixture', publicTaskId: 'task_fixture', usagePurpose: 'facts' };
      const request = plugin.buildSubmitRequest(ctx);
      assert.equal(request.method, 'POST');
      assert.ok(request.url.startsWith('https://upstream.example/'));
      const usage = plugin.extractUsage(ctx);
      assert.ok(Object.keys(usage).length > 0);
      if (key === 'doubao') {
        // Mapped endpoint IDs can only identify image work through its action.
        const mapped = { ...ctx, model: 'ep-image-fixture', upstreamModel: 'ep-image-fixture', requestBody: { ...ctx.requestBody, model: 'ep-image-fixture' } };
        assert.equal(plugin.buildSubmitRequest(mapped).url, 'https://upstream.example/api/v3/images/generations');
      }
    }
  });
}

test('Sunburst edits accept both async ID fields and render the completed provider image', async () => {
  const source = await readFile(new URL('./tasks/sunburst-images/plugin.js', import.meta.url), 'utf8');
  const plugin = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
  const ctx = { baseUrl: 'https://upstream.example', apiKey: 'fixture', authHeader: 'fixture', publicTaskId: 'gateway-id' };
  const url = 'https://cdn.example/image.png';
  const image = { data: [{ url }] };
  const edit = plugin.buildSubmitRequest({ ...ctx, model: 'mapped', requestBody: { operation: 'edit', body: { kind: 'json', value: {} } } });
  assert.equal(edit.headers.Authorization, 'Bearer fixture');
  for (const body of [{ id: 'abc' }, { task_id: 'abc' }, { id: '', task_id: 'abc', poll_url: 'https://ignored.example/' }]) {
    const accepted = plugin.parseSubmitResponse(ctx, { statusCode: 202, body });
    assert.equal(accepted.taskId, 'abc');
    assert.equal(accepted.immediate, undefined);
    const poll = plugin.buildQueryRequest({ ...ctx, taskId: accepted.taskId });
    assert.equal(poll.method, 'GET');
    assert.equal(poll.headers.Authorization, 'Bearer fixture');
    assert.equal(poll.url, 'https://upstream.example/v1/images/tasks/abc');
  }
  assert.throws(() => plugin.parseSubmitResponse(ctx, { statusCode: 202, body: {} }), /task ID/);
  assert.throws(() => plugin.buildQueryRequest({ ...ctx, taskId: '..' }), /task ID/);
  assert.equal(plugin.parseSubmitResponse(ctx, { statusCode: 200, body: image }).immediate.status, 'SUCCESS');
  assert.equal(plugin.parseTaskResult(ctx, { status: 'running', ...image }).status, 'IN_PROGRESS');
  assert.equal(plugin.parseTaskResult(ctx, { status: 'failed', ...image }).status, 'FAILURE');
  assert.throws(() => plugin.parseTaskResult(ctx, image, { status: 503 }), /503/);
  for (const body of [image, { result: image }, { image_url: url }]) {
    const completed = { status: 'completed', ...body };
    assert.equal(plugin.parseTaskResult(ctx, completed).url, url);
    assert.deepEqual(plugin.protocols.openai_image.render(ctx, { data: completed }), image);
  }
});
