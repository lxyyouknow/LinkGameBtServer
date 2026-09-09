import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {validateProject,checkProject} from './bt-project.mjs';
const baseline=checkProject();
assert.deepEqual(validateProject(baseline,{release:true}),[]);
assert.ok(validateProject({...baseline,environment:'production'},{release:true}).length);
assert.ok(validateProject({...baseline,runtime:'tiktok-native'},{release:true}).length);
const valid={...baseline,runtime:'tiktok-native',releaseEnabled:true,apiDomain:'bt-api.example.com',sshHost:'bt-host.example.com',sshUser:'btuser',sshKeyPath:'/tmp/fixture-key',remoteRoot:'/home/btuser/linkgame-bt-server',httpPort:24020,tiktokClientKey:'fixture-key',database:{host:'db.example.com',port:3306,name:'linkgame_bt_staging',user:'linkgame_bt_app'}};
assert.deepEqual(validateProject(valid,{release:true}),[]);
for(const mutate of [p=>p.releaseEnabled=false,p=>p.remoteRoot='/home/old/server',p=>p.remoteRoot='/home/linkgame-bt/../old',p=>p.database.name='linkgame',p=>p.database.port=70000,p=>p.sshHost='bt.example.invalid',p=>p.tiktokClientKey='',p=>p.password='fixture',p=>p.analyticsAppId=2]){const p=structuredClone(valid);mutate(p);assert.ok(validateProject(p,{release:true}).length);}
for(const file of ['release-production.sh','release-production-windows.ps1']){
 const code=readFileSync(new URL(file,import.meta.url),'utf8');
 assert.ok(code.indexOf('bt-project.mjs')<code.indexOf('scp'), '必须先检查再访问服务器');
 assert.ok(code.indexOf('deploy/check-target.sh')<code.indexOf('linkgame-bt-migrate',code.indexOf('deploy/check-target.sh')));
 assert.ok(code.includes('linkgame-bt-check-config'));
}
console.log('BT 服务端公开配置、发布前置检查和远端身份字段比较检查通过；未联网。');
