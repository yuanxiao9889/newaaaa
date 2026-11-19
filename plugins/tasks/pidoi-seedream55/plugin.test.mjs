import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
const source=await readFile(new URL('./plugin.js',import.meta.url),'utf8');
const plugin=await import('data:text/javascript;base64,'+Buffer.from(source).toString('base64'));
const context={model:'dou-5-5',upstreamModel:'seedream_5.5',baseUrl:'https://pidoi.example',apiKey:'fixture',publicTaskId:'task-local'};
function build(body,operation='generate') {
  const decoded=plugin.protocols.openai_image.decodeRequest({model:context.model,operation,body});
  return plugin.buildSubmitRequest({...context,requestBody:decoded.requestBody});
}
test('maps all six supported sizes and strips incompatible parameters',()=>{
  for(const [size,ratio] of Object.entries({'2048x2048':'1:1','2048x1152':'16:9','1152x2048':'9:16','2048x1536':'4:3','1536x2048':'3:4','2048x878':'21:9'})) {
    const out=build({kind:'json',value:{prompt:'draw',size,quality:'high',strength:'HIGH',watermark:false}});
    assert.equal(out.url,'https://pidoi.example/v1/images/generations');
    assert.deepEqual(out.body,{model:'seedream_5.5',prompt:'draw\n不要水印。',aspect_ratio:ratio,resolution:'2K',n:1,images:[]});
  }
});
test('old URL clients and new file-plus-URL clients preserve reference order',()=>{
  const images=Array.from({length:9},(_,i)=>'https://cdn.example/'+i+'.png');
  assert.deepEqual(build({kind:'json',value:{prompt:'edit',size:'2048x1152',image_urls:images}}).body.images,images);
  for(const count of [1,4,9]) {
    const refs=images.slice(0,count);
    const out=build({kind:'multipart',fields:{prompt:['edit'],size:['2048x1152'],oopii_reference_urls_v1:[JSON.stringify(refs)]},files:refs.map((_,i)=>({field:'image',ref:'request_file:image#'+i}))},'edit');
    assert.deepEqual(out.body.images,refs);assert.equal(out.bodyType,undefined);
  }
});
test('unsupported requests are rejected in decoder before channel admission',()=>{
  for(const value of [{size:'1024x1024'},{size:'2048x1365'},{resolution:'4K'},{size:'2048x1152',aspect_ratio:'1:1'},{image_url:'data:image/png;base64,AAA'},{images:Array(10).fill('https://cdn.example/a.png')},{n:2}])
    assert.throws(()=>build({kind:'json',value:{prompt:'draw',...value}}));
  assert.throws(()=>build({kind:'multipart',fields:{prompt:['edit'],size:['2048x1152']},files:[{field:'image',ref:'request_file:image'}]},'edit'));
});
test('URL and Base64 results preserve task identity; empty results fail',()=>{
  for(const entry of [{url:'https://cdn.example/out.png'},{b64_json:'cG5n'}]) {
    const body={data:[entry]};const result=plugin.parseSubmitResponse(context,{body});
    assert.equal(result.taskId,'task-local');assert.equal(result.immediate.status,'SUCCESS');assert.deepEqual(result.taskData,body);
  }
  assert.equal(plugin.parseSubmitResponse(context,{body:{data:[]}}).immediate.status,'FAILURE');
});
