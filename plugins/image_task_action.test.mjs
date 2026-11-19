import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const providers = [
  ['mai-seedream55', 'dou-5-5', 'seedream-5.5'],
  ['aicost-seedream55', 'dou-5-5', 'seedream-5.5'],
  ['pidoi-seedream55', 'dou-5-5', 'seedream_5.5'],
  ['duola-seedream55', 'dou-5-5', 'doubao-seedream-5-5-261028'],
  ['kuaiai-image25', 'all-image-2.5-sunburst-H'],
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
