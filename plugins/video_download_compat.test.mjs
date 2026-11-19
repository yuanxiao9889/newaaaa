import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

for (const key of ['huiqu-seedance20', 'huiqu-seedance25-dynamic', 'huiqu-h3-range', 'xing-wan30-prime']) {
  test(`${key} downloads signed extensionless video results without channel credentials`, async () => {
    const source = await readFile(new URL(`./tasks/${key}/plugin.js`, import.meta.url), 'utf8');
    const plugin = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
    for (const url of ['https://cdn.example/video/tos/result/?mime_type=video_mp4&sig=a%2Bb', 'https://cdn.example/video.mp4?sig=a%2Bb']) {
      for (const method of ['GET', 'HEAD']) {
        const result = plugin.buildContentRequest({
          artifactKey: 'video', upstreamTaskId: 'task-fixture', baseUrl: plugin.meta.baseUrl,
          apiKey: 'fixture-channel-secret', clientRequest: { method },
          data: { video_url: url, url, metadata: { url } },
        });
        assert.deepEqual(result, { url, method: 'GET', credentialless: true });
      }
    }
  });
}
