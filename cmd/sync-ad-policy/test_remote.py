import unittest, tempfile, pathlib, hashlib, json, os, stat
from remote import apply, validate

class SyncTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = pathlib.Path(self.temp.name)/'ad-policy.json'
        self.old = {'version':'old','enabled':True,'probabilities':{k:50 for k in ['level_entry_potion','empty_tool','no_pair','progress_chest','level_complete','daily_gift','daily_challenge_replay','season_makeup']}}
        self.path.write_text(json.dumps(self.old));self.path.chmod(0o640)
        self.old_bytes=self.path.read_bytes()
        self.new=json.loads(json.dumps(self.old));self.new['version']='new';self.new['probabilities']['empty_tool']=0
        self.request={'config':self.new,'expectedSha256':hashlib.sha256(self.old_bytes).hexdigest(),'expectedPid':'123'}
    def fetch(self):return validate(json.loads(self.path.read_text()))
    def test_success_and_repeat(self):
        result=apply(self.path,self.request,self.fetch,lambda:'123')
        self.assertEqual(result['status'],'synced');self.assertEqual(self.fetch(),self.new)
        self.assertEqual(pathlib.Path(result['backup']).read_bytes(),self.old_bytes)
        self.assertEqual(stat.S_IMODE(self.path.stat().st_mode),0o640)
        self.request['expectedSha256']=hashlib.sha256(self.path.read_bytes()).hexdigest()
        result=apply(self.path,self.request,self.fetch,lambda:'123')
        self.assertEqual(result['status'],'unchanged');self.assertEqual(len(list(self.path.parent.glob('*.bak-*'))),1)
    def test_rollback_after_validation_failure(self):
        count=0
        def broken():
            nonlocal count
            count+=1
            if count==2:raise ValueError('temporary network error')
            return self.fetch()
        with self.assertRaisesRegex(ValueError,'已恢复旧配置'):apply(self.path,self.request,broken,lambda:'123')
        self.assertEqual(self.path.read_bytes(),self.old_bytes)
    def test_racing_change_is_preserved(self):
        changed=self.old_bytes+b'\n';self.path.write_bytes(changed)
        with self.assertRaisesRegex(ValueError,'其他操作修改'):apply(self.path,self.request,self.fetch,lambda:'123')
        self.assertEqual(self.path.read_bytes(),changed)
    def test_process_changed_before_apply(self):
        with self.assertRaisesRegex(ValueError,'服务进程已变化'):apply(self.path,self.request,self.fetch,lambda:'456')
        self.assertEqual(self.path.read_bytes(),self.old_bytes)
    def test_invalid_config_does_not_write(self):
        self.new['probabilities']['empty_tool']=101
        with self.assertRaises(ValueError):apply(self.path,self.request,self.fetch,lambda:'123')
        self.assertEqual(self.path.read_bytes(),self.old_bytes)

if __name__=='__main__':unittest.main()
