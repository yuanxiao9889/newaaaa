import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
const plugins = {};
for (const name of ['mai-seedream55', 'aicost-seedream55']) {
 const text = await readFile(new URL('../'+name+'/plugin.js',import.meta.url),'utf8');
 plugins[name] = await import('data:text/javascript;base64,'+Buffer.from(text).toString('base64'));
}
const mai=plugins['mai-seedream55'], aicost=plugins['aicost-seedream55'];
const ctx={model:'dou-5-5',upstreamModel:'seedream-5.5',baseUrl:'https://upstream.example/v1/',apiKey:'fixture',publicTaskId:'task-local'};
function decode(plugin, value, body) {return plugin.protocols.openai_image.decodeRequest({model:ctx.model,operation:body?'edit':'generate',body:body||{kind:'json',value:{prompt:'draw',...value}}});}
function build(plugin,value,body) {return plugin.buildSubmitRequest({...ctx,requestBody:decode(plugin,value,body).requestBody});}
test('wide requests exclude MAI before submission while AICost accepts',()=>{
 for (const fields of [{size:'2048x878'},{size:'1792x768'},{size:'3024x1296'},{size:'3136x1344'},{aspect_ratio:'21:9'},{ratio:'7:3'}]) {
  assert.throws(()=>decode(mai,fields),/21:9/);
  const result=build(aicost,fields);assert.equal(result.body.model,'seedream-5.5');assert.ok(result.body.size);
 }
 const body={kind:'multipart',fields:{prompt:['edit'],size:['2048x878']},files:[{field:'image[]',ref:'request_file:image[]',filename:'ref.png'}]};
 assert.throws(()=>decode(mai,null,body),/21:9/);
 const result=build(aicost,null,body);assert.equal(result.url,'https://upstream.example/v1/images/edits');assert.equal(result.parts.find(p=>p.fileRef).fileRef,'request_file:image[]');
});
test('MAI preserves nonwide size conversion and URL reference order',()=>{
 const result=build(mai,{size:'2048x1152',quality:'high',image_urls:['https://cdn.example/1.png','https://cdn.example/2.png'],watermark:false});
 assert.deepEqual(result.body,{model:'seedream-5.5',prompt:'draw',n:1,async:true,stream:false,aspect_ratio:'16:9',quality:'2K',detail_level:'High',content:[{type:'image_url',role:'reference_image',image_url:{url:'https://cdn.example/1.png'}},{type:'image_url',role:'reference_image',image_url:{url:'https://cdn.example/2.png'}}],watermark:false});
 assert.equal(build(mai,{}).body.quality,'2K');
 assert.equal(build(mai,{size:'2048x2048'}).body.aspect_ratio,'1:1');
});

test('MAI sends explicit boolean watermark values from JSON and multipart',()=>{
 for (const watermark of [false,true]) {
  assert.equal(build(mai,{watermark}).body.watermark,watermark);
  const body={kind:'multipart',fields:{prompt:['edit'],watermark:[String(watermark)],image:['https://cdn.example/reference.png']}};
  const outgoing=build(mai,null,body).body;
  assert.equal(outgoing.watermark,watermark);
  assert.equal(outgoing.stream,false);
  assert.equal(outgoing.async,true);
  const fileBody={kind:'multipart',fields:{prompt:['edit'],watermark:[String(watermark)],size:['1152x2048'],oopii_reference_urls_v1:['["https://cdn.example/reference.jpg"]']},files:[{field:'image',ref:'request_file:image',filename:'image_1.jpg'}]};
  const fileOutgoing=build(mai,null,fileBody).body;
  assert.equal(fileOutgoing.watermark,watermark);
  assert.equal(fileOutgoing.aspect_ratio,'9:16');
  assert.deepEqual(fileOutgoing.content,[{type:'image_url',role:'reference_image',image_url:{url:'https://cdn.example/reference.jpg'}}]);
 }
 assert.equal(Object.hasOwn(build(mai,{}).body,'watermark'),false);
 assert.equal(Object.hasOwn(build(mai,null,{kind:'multipart',fields:{prompt:['edit']}}).body,'watermark'),false);
 for (const watermark of ['false','true','',0,1,null,[],{}]) assert.throws(()=>build(mai,{watermark}),/watermark must be true or false/);
 for (const watermark of ['','0','1','False','yes','null']) assert.throws(()=>build(mai,null,{kind:'multipart',fields:{prompt:['edit'],watermark:[watermark]}}),/watermark must be true or false/);
});
test('reference source mismatches and quantity bypasses are rejected',()=>{
 for(const plugin of [mai,aicost])for(const n of [0,-1,2,129,'18446744073686646784'])assert.throws(()=>decode(plugin,{n}),/n=1/);
 const body={kind:'multipart',fields:{prompt:['edit'],size:['2048x1152'],oopii_reference_urls_v1:['["https://cdn.example/1.png"]']},files:[{field:'image',ref:'request_file:image',filename:'r.png'}]};
 assert.equal(build(mai,null,body).body.content[0].image_url.url,'https://cdn.example/1.png');
 assert.throws(()=>decode(mai,null,{...body,fields:{...body.fields,oopii_reference_urls_v1:['[]']}}),/count/);
 assert.throws(()=>decode(aicost,{image:['https://cdn.example/1.png'],image_url:'https://cdn.example/2.png'}),/one reference/);
});
test('upstream mapping stays editable and AICost strips internal queue fields',()=>{
 const decoded=decode(aicost,{size:'2048x878',async:true,stream:false,oopii_reference_urls_v1:'[]'});
 const result=aicost.buildSubmitRequest({...ctx,upstreamModel:'new-upstream-id',requestBody:decoded.requestBody});
 assert.equal(result.body.model,'new-upstream-id');assert.equal(result.body.async,undefined);assert.equal(result.body.oopii_reference_urls_v1,undefined);
});

test('AICost prefers matching companion URLs and preserves reference order and scalar types',()=>{
 const urls=['https://cdn.example/second.jpg?e=123&token=fixture','https://cdn.example/first.jpg'];
 const files=urls.map((_,i)=>({field:'image[]',ref:'request_file:image[]'+(i?'#'+i:''),filename:'r'+i+'.jpg'}));
 const body={kind:'multipart',fields:{prompt:['edit'],n:['1'],size:['1152x2048'],watermark:['false'],seed:['0'],output_compression:['90'],oopii_reference_urls_v1:[JSON.stringify(urls)]},files};
 const result=build(aicost,null,body);
 assert.equal(decode(aicost,null,body).action,'image_edit');
 assert.equal(decode(aicost,{}).action,'image_generate');
 assert.equal(decode(aicost,{image:urls}).action,'image_edit');
 assert.equal(decode(aicost,null,{kind:'multipart',fields:{prompt:['edit']},files}).action,'image_edit');
 assert.equal(result.url,'https://upstream.example/v1/images/edits');
 assert.equal(result.headers['Content-Type'],'application/json');
 assert.equal(result.bodyType,undefined);
 assert.equal(result.parts,undefined);
 assert.deepEqual(result.body,{model:'seedream-5.5',prompt:'edit',n:1,size:'1152x2048',watermark:false,seed:0,output_compression:90,image:urls,response_format:'url'});
 for (const invalid of ['broken','{}','[]',JSON.stringify([urls[0]]),JSON.stringify([urls[0],'data:image/jpeg;base64,AAAA']),JSON.stringify([urls[0],'https://user:password@cdn.example/a.jpg'])]) {
  assert.throws(()=>build(aicost,null,{...body,fields:{...body.fields,oopii_reference_urls_v1:[invalid]}}),/Reference URL/);
 }
 const fallback=build(aicost,null,{...body,fields:{prompt:['edit'],watermark:['false']}});
 assert.equal(fallback.bodyType,'multipart');
 assert.deepEqual(fallback.parts.filter(p=>p.fileRef).map(p=>p.fileRef),files.map(f=>f.ref));
 assert.equal(fallback.parts.find(p=>p.name==='watermark').value,'false');
 for (const watermark of ['false','true',0,null]) assert.throws(()=>build(aicost,{watermark}),/watermark must be true or false/);
 for (const fields of [{seed:['NaN']},{seed:['9007199254740992']},{output_compression:['101']}]) assert.throws(()=>build(aicost,null,{...body,fields:{...body.fields,...fields}}),/must be/);
});
test('async task statuses, failure, unknown status and polling ID validation',()=>{
 const response=mai.parseSubmitResponse(ctx,{statusCode:202,body:{id:'task-vendor',status:'queued'}});
 assert.equal(response.immediate,undefined);assert.equal(response.taskId,'task-vendor');
 assert.equal(mai.buildQueryRequest({...ctx,taskId:response.taskId}).url,'https://upstream.example/v1/images/generations/task-vendor');
 assert.throws(()=>mai.buildQueryRequest({...ctx,taskId:'../escape'}),/Invalid/);
 assert.equal(mai.parseTaskResult(ctx,{status:'in_progress'}).status,'IN_PROGRESS');
 assert.equal(mai.parseTaskResult(ctx,{status:'unexpected'}).status,'UNKNOWN');
 assert.equal(mai.parseTaskResult(ctx,{status:'failed',error:{message:'failure'}}).status,'FAILURE');
 const data={status:'completed',data:[{url:'https://cdn.example/result.png'}]};
 const result=mai.parseTaskResult(ctx,data);assert.equal(result.status,'SUCCESS');assert.deepEqual(mai.extractUsageOnComplete(ctx,result,data),{image_count:1});
});
test('AICost immediate output keeps URL without duplicate Base64 and usage is one image',()=>{
 const body={created:123,usage:{prompt_tokens:20,completion_tokens:30,total_tokens:50},data:[{url:'https://cdn.example/result.png',b64_json:'large-base64'}]};
 const response=aicost.parseSubmitResponse(ctx,{statusCode:200,body});assert.equal(response.immediate.status,'SUCCESS');assert.equal(response.taskData.data[0].b64_json,undefined);
 assert.deepEqual(response.immediate.tokenUsage,body.usage);assert.deepEqual(aicost.extractUsageOnSubmit(ctx,body),{image_count:1});
 assert.equal(aicost.parseSubmitResponse(ctx,{statusCode:200,body:{error:{message:'no image'}}}).immediate.status,'FAILURE');
});
