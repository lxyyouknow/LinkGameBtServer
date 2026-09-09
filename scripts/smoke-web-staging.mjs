#!/usr/bin/env node
// 经授权的新 staging 冒烟；通过本机 SSH 隧道连接，Token/密码只在内存中使用。
import assert from 'node:assert/strict';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {resolve,join} from 'node:path';
import {randomUUID} from 'node:crypto';
const root=resolve(import.meta.dirname,'..');
const project=JSON.parse(readFileSync(join(root,'config/project.json')));
assert.equal(project.runtime,'web-preview');assert.equal(project.environment,'staging');
const secrets=JSON.parse(readFileSync(join(root,'.env.deploy.local.json')));
const base='http://127.0.0.1:23301';
async function api(path,body,token,method=body?'POST':'GET'){
 const r=await fetch(base+path,{method,headers:{'Content-Type':'application/json',...(token?{Authorization:`Bearer ${token}`}:{})},body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(15000)});
 const value=await r.json();if(!r.ok)throw new Error(`${path}: ${r.status} ${value.error?.code||''}`);return value;
}
const account=`btqa_${Date.now()}`;
const login=await api('/v1/auth/test-account',{account});
const second=await api('/v1/auth/test-account',{account});assert.equal(login.playerId,second.playerId);
const other=await api('/v1/auth/test-account',{account:`${account}_b`});assert.notEqual(login.playerId,other.playerId);
const save=await api('/v1/save',null,login.token);assert.equal(save.hintCount,0);
const fields=['revision','level','selectedTheme','collectingTheme','soundEnabled','musicEnabled','effectsEnabled','vibrationEnabled','tutorialCompleted'];
const update=Object.fromEntries(fields.map(k=>[k,save[k]]));update.level=2;update.tutorialCompleted=true;update.clientVersion='0.1.0';
await api('/v1/save',update,login.token,'PUT');
assert.equal((await api('/v1/save',null,second.token)).level,2);
assert.equal((await api('/v1/save',null,other.token)).level,save.level);
const session=await api('/v1/ads/rewarded/session',{placement:'hint',businessKey:`qa:${randomUUID()}`},login.token);
const claim={sessionId:session.sessionId,requestId:randomUUID(),adAttemptId:randomUUID()};
const reward=await api('/v1/ads/rewarded/claim',claim,login.token);assert.equal(reward.save.hintCount,3);
const retry=await api('/v1/ads/rewarded/claim',claim,login.token);assert.equal(retry.save.hintCount,3);
assert.equal((await api('/v1/save',null,login.token)).hintCount,3);
for(const path of ['/v1/daily-gift','/v1/daily-challenge','/v1/season/current','/v1/leaderboards/global'])await api(path,null,login.token);
const gameSessionId=randomUUID();
const event=(name,extra={})=>({id:randomUUID(),name,eventTime:new Date().toISOString(),appId:1,sdkType:0,channel:0,clientVersion:'0.1.0',platform:'web',gameSessionId,...extra});
const events=[event('session_start'),event('enter_game'),event('online_time',{durationSeconds:15}),event('online_time',{durationSeconds:15}),event('online_time',{durationSeconds:7})];
for(let i=0;i<3;i++){const ack=await api('/v1/analytics/events',{events},login.token);assert.equal(ack.acceptedEventIds.length,events.length);}
const gm=await api('/api/gm/login',{account:secrets.gmAccount,password:secrets.gmPassword});
const date=new Intl.DateTimeFormat('en-CA',{timeZone:'Asia/Tokyo',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date());
const filter={from:date,to:date,appId:1,sdkType:0,channel:0};
const reports={};for(const name of ['overview','ranking','ads','startup','daily','overview-trend'])reports[name]=(await api(`/api/gm/stats/${name}`,filter,gm.session)).data;
assert.equal(reports.ranking.find(p=>p.playerId===login.playerId)?.onlineDuration,37,'同一组事件上传三次后，累计在线仍须为 37 秒');
await api('/api/gm/logout',{},gm.session);
const output={date,account,checks:{loginStableIdentity:true,accountsIsolated:true,saveAcrossSessions:true,rewardClaimIdempotent:true,featureQueries:true,analyticsTripleReplayAcknowledged:true,gmReports:true},expectedOnlineSeconds:37,playerId:login.playerId,reports};
mkdirSync(join(root,'output'),{recursive:true});writeFileSync(join(root,'output/web-staging-smoke.json'),JSON.stringify(output,null,2)+'\n');
console.log(JSON.stringify({date,account,checks:output.checks,expectedOnlineSeconds:37},null,2));
