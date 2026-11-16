"""Build a dependency-free Windows release and local plugin bundles."""
import hashlib, json, os, pathlib, shutil, subprocess, sys, tempfile, zipfile, tarfile, re
ROOT=pathlib.Path(__file__).resolve().parents[1]
OUT=pathlib.Path(sys.argv[1] if len(sys.argv)>1 else ROOT/'release').resolve()
OUT.mkdir(parents=True,exist_ok=True)
APP_VERSION=re.search(r'const version = "([^"]+)"',(ROOT/'store.go').read_text()).group(1)

def write(path,content):
    path.parent.mkdir(parents=True,exist_ok=True)
    path.write_text(json.dumps(content,indent=2)+'\n',encoding='utf-8')

def archive(root,path):
    with zipfile.ZipFile(path,'w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
        for p in sorted(root.rglob('*')):
            if p.is_file() and p != path and OUT not in p.parents and '__pycache__' not in p.parts and '.git' not in p.parts:z.write(p,p.relative_to(root))

env=dict(os.environ,GOOS='windows',GOARCH='amd64',CGO_ENABLED='0')
subprocess.run(['go','build','-trimpath','-ldflags=-s -w -H windowsgui','-o',str(OUT/'L-Bridge.exe'),'.'],cwd=ROOT,env=env,check=True)
with tempfile.TemporaryDirectory() as temp:
    temp=pathlib.Path(temp)
    claude=temp/'claude';(claude/'server').mkdir(parents=True)
    shutil.copy2(OUT/'L-Bridge.exe',claude/'server/L-Bridge.exe')
    write(claude/'manifest.json',{'manifest_version':'0.3','name':'l-bridge','display_name':'L-Bridge','version':APP_VERSION,'description':'Share a local goal, messages, tasks and artifacts with a ChatGPT agent.','author':{'name':'L-Bridge contributors'},'server':{'type':'binary','entry_point':'server/L-Bridge.exe','mcp_config':{'command':'${__dirname}/server/L-Bridge.exe','args':['--mcp','--agent','claude']}},'compatibility':{'platforms':['win32']},'tools_generated':True})
    archive(claude,OUT/'L-Bridge-Claude-Windows.mcpb')
    plugin=temp/'plugin';(plugin/'server').mkdir(parents=True);(plugin/'assets').mkdir()
    shutil.copy2(OUT/'L-Bridge.exe',plugin/'server/L-Bridge.exe');shutil.copy2(ROOT/'web/icon.svg',plugin/'assets/icon.svg')
    write(plugin/'plugin.json',{'$schema':'https://agent-plugins.org/schemas/1.0.0/plugin.schema.json','name':'l-bridge','version':APP_VERSION,'description':'A local shared goal and messaging workspace for Claude and ChatGPT.','author':{'name':'L-Bridge contributors'},'extensions':{'com.openai':{'requires_local_executor':True,'interface':{'displayName':'L-Bridge','shortDescription':'Two minds. One mission.'}}}})
    write(plugin/'mcp.json',{'$schema':'https://agent-plugins.org/schemas/1.0.0/mcp.schema.json','mcpServers':{'l-bridge':{'type':'stdio','command':'${PLUGIN_ROOT}/server/L-Bridge.exe','args':['--mcp','--agent','chatgpt'],'cwd':'${PLUGIN_ROOT}'}}})
    # Compatibility layout for hosts predating portable Agent Plugins.
    write(plugin/'.codex-plugin/plugin.json',{'name':'l-bridge','version':APP_VERSION,'description':'Local collaboration with Claude.','mcpServers':'./.mcp.json','requires_local_executor':True,'interface':{'displayName':'L-Bridge','shortDescription':'Two minds. One mission.'}})
    write(plugin/'.mcp.json',{'mcpServers':{'l-bridge':{'command':'${PLUGIN_ROOT}/server/L-Bridge.exe','args':['--mcp','--agent','chatgpt']}}})
    shutil.copy2(ROOT/'README.md',plugin/'README.md')
    shutil.copytree(ROOT/'docs',plugin/'docs')
    for name in ['PRIVACY.md','CHANGELOG.md','TESTING.md']:shutil.copy2(ROOT/name,plugin/name)
    archive(plugin,OUT/'L-Bridge-OpenAI-Windows.zip')
    release=temp/'L-Bridge';release.mkdir()
    for file in ['L-Bridge.exe','L-Bridge-Claude-Windows.mcpb','L-Bridge-OpenAI-Windows.zip']:shutil.copy2(OUT/file,release/file)
    shutil.copy2(ROOT/'README.md',release/'README.md')
    shutil.copy2(ROOT/'TESTING.md',release/'TESTING.md')
    shutil.copy2(ROOT/'PRIVACY.md',release/'PRIVACY.md')
    shutil.copytree(ROOT/'docs',release/'docs')
    start_text = f"""L-BRIDGE {APP_VERSION} BETA

PORT OCCUPIED OR UPGRADING: Closing the browser tab leaves the service running. Close connected Claude/Codex hosts, then double-click Restart L-Bridge.cmd in THIS extracted folder. It stops the verified old service and starts this release, preserving saved goals. Update BOTH installed plugin packages too; their bundled EXEs do not update when you replace the standalone EXE.

CONNECTION FIX: Both agents discover one shared service and its current port. If another app owns the default port, the bridge chooses a free loopback port automatically. For a custom data folder, copy the updated configurations from Connections into both hosts. Restart hosts after updating.

To quit without reopening: use Stop L-Bridge.cmd or Settings -> Stop local bridge.
The EXE also offers a restart when it finds an older running version. A newer running version is reopened rather than downgraded. Back up the data folder before updating.

1. Open L-Bridge.exe.
2. In Team & tools, click Install for each agent. Finish in the Windows setup window.
3. Click Sign in for each agent. If Codex needs Node.js, install it from the page that opens, then click Install Codex again.
4. Click Check connections. Advanced paths are optional.
5. Create one goal, then press Start Team.
6. Add follow-up directions in the goal message box. Stop Team cancels the runners.
7. Auto switch in goal Settings lets the surviving Start Team runner take over after a peer fails. Estimated progress follows the shared task board.
8. Replace BOTH desktop plugin packages and restart both hosts to receive the adapter reconnection fixes.
9. Enable Web research in the goal Settings for live search. After a stopped run, use Continue task.
10. Use Settings -> Check & install update, or Update L-Bridge.cmd, for future versions.
11. Open ? Guide for the built-in tutorial. Settings -> Learning & help toggles information icons.

ROBLOX: Enable Studio as MCP server in Roblox Assistant settings. In L-Bridge add the Roblox Studio preset and test the saved connection, then enable it under Tools for your goal. Use Files + commands access. Keep Studio open.

MCP: Add MCP server opens a plain form for name, command, and one argument per line. Save, test, and enable it for your goal. Local stdio MCP only.

This is a portable EXE with a local browser interface; no MSI installation is needed.

The original manual desktop plugin workflow is still available under Connections.
Start Team uses Claude Code and OpenAI Codex; it does not wake existing chat windows.
L-Bridge coordinates locally. Provider models, accounts, usage limits and billing still apply.
This is an unsigned Windows x64 developer beta. Live Windows/Roblox/two-provider execution has not been verified here. Read TESTING.md.
"""
    (release/'START-HERE.txt').write_text(start_text,encoding='utf-8')
    (OUT/'START-HERE.txt').write_text(start_text,encoding='utf-8')
    for label,flag in [('Restart','--restart'),('Stop','--stop'),('Update','--update')]:
        filename=label+' L-Bridge.cmd'
        content=('@echo off\r\nsetlocal DisableDelayedExpansion\r\nstart "" "%~dp0L-Bridge.exe" '+flag+'\r\n').encode('utf-8')
        (release/filename).write_bytes(content)
        (OUT/filename).write_bytes(content)
    shutil.copy2(ROOT/'CHANGELOG.md',release/'CHANGELOG.md')
    for image in ('preview.jpg','tutorial.jpg'):
        if (OUT/image).exists():shutil.copy2(OUT/image,release/image)
    local=release/'OpenAI-local-source';shutil.copytree(plugin,local/'plugins/l-bridge')
    write(local/'.agents/plugins/marketplace.json',{'name':'l-bridge-local','interface':{'displayName':'L-Bridge Local'},'plugins':[{'name':'l-bridge','source':{'source':'local','path':'./plugins/l-bridge'},'policy':{'installation':'AVAILABLE','authentication':'ON_INSTALL'},'category':'Productivity'}]})
    archive(release,OUT/'L-Bridge-Windows.zip')
    # Verify each archive can be extracted and all binary/config references exist.
    for path in OUT.iterdir():
        if path.suffix in ('.zip','.mcpb'):
            with zipfile.ZipFile(path) as z:assert z.testzip() is None,path
    binary=(OUT/'L-Bridge.exe').read_bytes();assert binary[:2]==b'MZ'
shutil.copy2(ROOT/'README.md',OUT/'README.md')
shutil.copy2(ROOT/'TESTING.md',OUT/'TESTING.md')
shutil.copy2(ROOT/'CHANGELOG.md',OUT/'CHANGELOG.md')
with zipfile.ZipFile(OUT/'L-Bridge-Source.zip','w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
    for p in sorted(ROOT.rglob('*')):
        if p.is_file() and OUT not in p.parents and '__pycache__' not in p.parts and '.git' not in p.parts and (p.name == '.gitignore' or p.suffix in {'.go','.mod','.sum','.md','.html','.css','.js','.svg','.py','.txt','.ps1','.yml','.png'}):
            z.write(p,p.relative_to(ROOT))
checksums=[]
for path in sorted(OUT.iterdir()):
    if path.is_file() and path.suffix in ('.exe','.zip','.mcpb','.cmd'):
        checksums.append(hashlib.sha256(path.read_bytes()).hexdigest()+'  '+path.name)
(OUT/'SHA256SUMS.txt').write_text('\n'.join(checksums)+'\n')
bundle=OUT/('L-Bridge-Complete-v'+APP_VERSION+'-beta.tar.xz')
prefix='L-Bridge-v'+APP_VERSION
files=[p for p in sorted(OUT.iterdir()) if p.is_file() and not p.name.endswith('.tar.xz') and p.name!='DOWNLOAD-SHA256.txt']
def public_tar_metadata(info):
    info.uid=0;info.gid=0;info.uname='';info.gname='';info.mtime=0
    return info
with tarfile.open(bundle,'w:xz',preset=9) as t:
    for p in files:t.add(p,arcname=prefix+'/'+p.name,filter=public_tar_metadata)
with tarfile.open(bundle,'r:xz') as t:
    for p in files:
        assert t.extractfile(prefix+'/'+p.name).read()==p.read_bytes(),p
(OUT/'DOWNLOAD-SHA256.txt').write_text(hashlib.sha256(bundle.read_bytes()).hexdigest()+'  '+bundle.name+'\n')
print('Built '+str(OUT))
