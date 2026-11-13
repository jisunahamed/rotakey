import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { once } from 'node:events';
import { createProxy } from './server.mjs';

const key='isolated-test-key-0123456789';
const listen=async s=>{s.listen(0,'127.0.0.1');await once(s,'listening');return `http://127.0.0.1:${s.address().port}`;};
test('preserves full payload, upstream errors and successful empty responses',async t=>{
 let status=429,received;
 const upstream=http.createServer(async(req,res)=>{const chunks=[];for await(const c of req)chunks.push(c);received=Buffer.concat(chunks).toString();assert.equal(req.url,'/zen/v1/chat/completions');assert.equal(req.headers.authorization,'Bearer public');res.writeHead(status,{'content-type':'application/json','retry-after':'3'});res.end(status===200?'':'{"error":{"message":"real rejection"}}');});
 const origin=await listen(upstream);const proxy=createProxy({apiKey:key,origin});const url=await listen(proxy);
 t.after(()=>{proxy.closeAllConnections();proxy.close();upstream.closeAllConnections();upstream.close();});
 const body=JSON.stringify({model:'big-pickle',messages:[],max_tokens:123,temperature:0.2,stream:true});
 let r=await fetch(url+'/v1/chat/completions',{method:'POST',headers:{authorization:`Bearer ${key}`},body});assert.equal(r.status,429);assert.equal(r.headers.get('retry-after'),'3');assert.equal(await r.text(),'{"error":{"message":"real rejection"}}');assert.equal(received,body);
 status=200;r=await fetch(url+'/v1/chat/completions',{method:'POST',headers:{authorization:`Bearer ${key}`},body});assert.equal(r.status,200);assert.equal(await r.text(),'');
 r=await fetch(url+'/v1/models');assert.equal(r.status,401);
});
test('disconnect cancels upstream and does not replay',async t=>{
 let calls=0;let resolveClose;const closed=new Promise(r=>resolveClose=r);
 const upstream=http.createServer(async(req,res)=>{calls++;req.resume();res.writeHead(200,{'content-type':'text/event-stream'});res.write(': hello\n\n');res.once('close',resolveClose);});
 const origin=await listen(upstream);const proxy=createProxy({apiKey:key,origin});const url=await listen(proxy);
 t.after(()=>{proxy.closeAllConnections();proxy.close();upstream.closeAllConnections();upstream.close();});
 const controller=new AbortController();const r=await fetch(url+'/v1/chat/completions',{method:'POST',headers:{authorization:`Bearer ${key}`},body:'{"model":"big-pickle"}',signal:controller.signal});
 const reader=r.body.getReader();assert.equal((await reader.read()).done,false);controller.abort();
 await Promise.race([closed,new Promise((_,reject)=>{const timer=setTimeout(()=>reject(Error('upstream not cancelled')),2000);timer.unref();})]);assert.equal(calls,1);
});
