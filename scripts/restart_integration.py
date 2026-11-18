"""Upgrade a real older service and verify saved goals survive.
Usage: python3 scripts/restart_integration.py OLD_EXECUTABLE NEW_EXECUTABLE
"""
import json, pathlib, subprocess, sys, tempfile, time, urllib.request
old_exe,new_exe=[str(pathlib.Path(p).resolve()) for p in sys.argv[1:3]]
processes=[]
with tempfile.TemporaryDirectory(prefix='lbridge-restart-') as temp:
    directory=pathlib.Path(temp);endpoint=directory/'endpoint.json'
    def wait_endpoint(version=None,old_token=None):
        deadline=time.monotonic()+12
        while time.monotonic()<deadline:
            try:
                e=json.loads(endpoint.read_text())
                if old_token and e['token']==old_token:time.sleep(.05);continue
                req=urllib.request.Request(e['url']+'/api/state',headers={'Authorization':'Bearer '+e['token']})
                with urllib.request.urlopen(req,timeout=1) as r:
                    state=json.load(r)
                    if version is None or state['version']==version:return e,state
            except (OSError,ValueError):pass
            time.sleep(.05)
        raise AssertionError('service did not become ready')
    def post(e,path,body):
        req=urllib.request.Request(e['url']+'/api/'+path,data=json.dumps(body).encode(),headers={'Authorization':'Bearer '+e['token'],'Content-Type':'application/json'})
        with urllib.request.urlopen(req,timeout=5) as r:return json.load(r)
    try:
        old=subprocess.Popen([old_exe,'--serve','--no-open','--data-dir',temp,'--port','0'],stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,text=True);processes.append(old)
        e,state=wait_endpoint();old_version=state['version']
        gid=post(e,'create',{'title':'Preserve this goal','prompt':'Saved before upgrading.','mode':'files','limit':120})['id']
        post(e,'message',{'goal_id':gid,'body':'This message must survive the restart.'})
        port=e['url'].rsplit(':',1)[1]
        new=subprocess.Popen([new_exe,'--restart','--serve','--no-open','--data-dir',temp,'--port',port],stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,text=True);processes.append(new)
        ready,state=wait_endpoint('0.4.2',e['token']);old.wait(timeout=5)
        assert old.returncode==0,old.stderr.read()
        goal=next(g for g in state['goals'] if g['id']==gid)
        assert goal['prompt']=='Saved before upgrading.'
        assert goal['messages'][-1]['body']=='This message must survive the restart.'
        # Relaunch reuses the service rather than competing for its port.
        again=subprocess.run([new_exe,'--no-open','--data-dir',temp,'--port',port],capture_output=True,text=True,timeout=5)
        assert again.returncode==0,again.stderr
        assert json.loads(endpoint.read_text())['pid']==ready['pid']
        stop=subprocess.run([new_exe,'--stop','--data-dir',temp],capture_output=True,text=True,timeout=12)
        assert stop.returncode==0,stop.stderr
        new.wait(timeout=5);assert new.returncode==0,new.stderr.read()
        assert not endpoint.exists()
        saved=json.loads((directory/'state.json').read_text())
        assert saved['goals'][0]['id']==gid
        print(json.dumps({'result':'PASS','from':old_version,'to':'0.4.2','checks':['authenticated graceful upgrade','same port reused','saved goal and messages retained','duplicate launch reuses service','stop command ends service','saved state retained after stop']}))
    finally:
        for p in processes:
            if p.poll() is None:p.terminate();p.wait(timeout=5)
