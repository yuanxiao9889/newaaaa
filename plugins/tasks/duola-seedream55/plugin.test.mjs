import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
const source = await readFile(new URL('./plugin.js', import.meta.url), 'utf8');
const plugin = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
const context = { model: 'dou-5-5', upstreamModel: 'doubao-seedream-5-5-261028', baseUrl: 'https://upstream.example/v1/', apiKey: 'fixture', publicTaskId: 'task-fixture' };
function build(body, operation = 'generate') {
  const decoded = plugin.protocols.openai_image.decodeRequest({ model: context.model, operation, body });
  return plugin.buildSubmitRequest({ ...context, requestBody: decoded.requestBody });
}
test('text generation preserves dimensions and forces the verified watermark policy', () => {
  const result = build({ kind: 'json', value: { model: context.model, prompt: 'draw', n: 1, size: '2048x1152', watermark: true, strength: 'HIGH', quality: 'medium' } });
  assert.equal(result.url, 'https://upstream.example/v1/images/generations');
  assert.deepEqual(result.body, { model: context.upstreamModel, n: 1, prompt: 'draw\n不要水印。', watermark: false, response_format: 'url', size: '2048x1152' });
});
test('single and repeated file references use edits, keep order and encode false as text', () => {
  for (const count of [1, 4]) {
    const files = Array.from({length: count}, (_,i) => ({ field:'image[]', ref:'request_file:image[]'+(i?'#'+i:''), filename:i+'.png' }));
    const result = build({kind:'multipart',fields:{model:[context.model],prompt:['edit\n不要水印。'],n:['1'],size:['2048x1152'],watermark:['true'],oopii_reference_urls_v1:['["https://cdn.example/reference.png"]']},files},'edit');
    assert.equal(result.url, 'https://upstream.example/v1/images/edits');
    assert.equal(result.bodyType, 'multipart');
    assert.deepEqual(result.parts.filter(x=>x.fileRef),files.map(x=>({name:'image',fileRef:x.ref,filename:x.filename})));
    assert.deepEqual(result.parts.find(x=>x.name==='watermark'),{name:'watermark',value:'false'});
    assert.equal(result.parts.find(x=>x.name==='prompt').value,'edit\n不要水印。');
    assert.equal(result.parts.some(x=>x.name==='oopii_reference_urls_v1'),false);
  }
});
test('old URL-reference clients remain compatible without claiming the file-edits watermark fix', () => {
  for (const field of ['image_url','image_urls','image']) {
    const result=build({kind:'json',value:{prompt:'edit',[field]:field==='image_url'?'https://cdn.example/a.png':['https://cdn.example/a.png']}});
    assert.equal(result.url,'https://upstream.example/v1/images/generations');
    assert.deepEqual(result.body.image,['https://cdn.example/a.png']);
    assert.equal(result.body.watermark,false);
  }
});
test('invalid quantity, unsupported files, ambiguity and reference overflow reject before submit', () => {
  assert.throws(()=>build({kind:'json',value:{prompt:'draw',n:2}}));
  assert.throws(()=>build({kind:'multipart',fields:{prompt:['draw']},files:[{field:'mask',ref:'request_file:mask'}]},'edit'));
  assert.throws(()=>build({kind:'multipart',fields:{prompt:['draw'],image_url:['https://cdn.example/a.png']},files:[{field:'image',ref:'request_file:image'}]},'edit'));
  assert.throws(()=>build({kind:'multipart',fields:{prompt:['draw']},files:Array.from({length:11},()=>({field:'image',ref:'request_file:image'}))},'edit'));
});
test('synchronous completion reuses the durable task ID and missing results fail', () => {
  const body={data:[{url:'https://cdn.example/output.png'}]};
  const result=plugin.parseSubmitResponse(context,{body});
  assert.equal(result.taskId,context.publicTaskId);assert.equal(result.immediate.status,'SUCCESS');
  assert.equal(plugin.extractUsageOnSubmit(context,body).image_count,1);
  assert.equal(plugin.parseSubmitResponse(context,{body:{data:[]}}).immediate.status,'FAILURE');
});
