"""Exercise two real stdio MCP processes, with no model calls."""
import json, os, pathlib, subprocess, sys, tempfile, urllib.request
exe = str(pathlib.Path(sys.argv[1]).resolve())
endpoint = pathlib.Path(sys.argv[2]).resolve()
e = json.loads(endpoint.read_text())
processes=[]
class Client:
    def __init__(self, agent):
        self.agent=agent; self.seq=0
        self.p=subprocess.Popen([exe,'--mcp','--agent',agent,'--data-dir',str(endpoint.parent)],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        processes.append(self.p)
        response=self.rpc('initialize',{'protocolVersion':'2025-06-18','capabilities':{},'clientInfo':{'name':'integration','version':'1'}})
        assert response['result']['protocolVersion']=='2025-06-18'
        self.p.stdin.write(json.dumps({'jsonrpc':'2.0','method':'notifications/initialized'})+'\n');self.p.stdin.flush()
        assert len(self.rpc('tools/list',{})['result']['tools'])==11
    def rpc(self, method, params):
        self.seq+=1
        self.p.stdin.write(json.dumps({'jsonrpc':'2.0','id':self.seq,'method':method,'params':params})+'\n');self.p.stdin.flush()
        result=json.loads(self.p.stdout.readline());assert result['id']==self.seq,result
        return result
    def call(self,name,**args):
        result=self.rpc('tools/call',{'name':name,'arguments':args})['result']
        assert not result.get('isError'),result
        return json.loads(result['content'][0]['text'])
try:
    claude,chatgpt=Client('claude'),Client('chatgpt')
    goal=claude.call('bridge_create',title='Protocol integration check',prompt='Create and review a small text artifact.')['goal_id']
    for c in [claude,chatgpt]:c.call('bridge_join',goal_id=goal,role='Builder' if c.agent=='claude' else 'Reviewer')
    start=chatgpt.call('bridge_sync',goal_id=goal)['cursor']
    claude.call('bridge_send',goal_id=goal,body='I will write the artifact.',summary='Taking the first draft.',nonce='integration-1')
    update=chatgpt.call('bridge_sync',goal_id=goal,after=start)
    assert len(update['messages'])==1 and update['messages'][0]['from']=='claude'
    artifact=claude.call('bridge_files',goal_id=goal,op='write',path='result.txt',content='A shared result.\n',expected_hash='')
    read=chatgpt.call('bridge_files',goal_id=goal,op='read',path='result.txt')
    assert read['hash']==artifact['hash'] and read['content']=='A shared result.\n'
    chatgpt.call('bridge_summary',goal_id=goal,text='Artifact written and independently read through the second client. Protocol test only; no LLM inference.',expected_version=0)
    claude.call('bridge_finish',goal_id=goal,evidence='Artifact saved successfully.')
    result=chatgpt.call('bridge_finish',goal_id=goal,evidence='Read the file; hash and content matched.')
    assert result['status']=='completed'
    print(json.dumps({'result':'PASS','goal':goal,'checks':['two MCP processes','initialize','tools/list','cross-agent delta','artifact handoff','completion handshake']}))
finally:
    for p in processes:
        p.stdin.close()
        p.wait(timeout=5)
        assert p.returncode==0,p.stderr.read()
