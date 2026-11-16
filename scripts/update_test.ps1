# Exercise updater decisions and real file replacement without network or processes.
$ErrorActionPreference = 'Stop'
$program = Join-Path $PSScriptRoot '../update.ps1'
foreach ($scenario in @('current','missing-digest','bad-checksum','busy','healthy','rollback')) {
  & {
    param($scenario, $program)
    $root = Join-Path ([IO.Path]::GetTempPath()) ('lbridge-update-test-'+[guid]::NewGuid())
    New-Item -ItemType Directory -Path $root | Out-Null
    $target = Join-Path $root 'L-Bridge.exe'
    $dataDir = Join-Path $root 'data'
    New-Item -ItemType Directory -Path $dataDir | Out-Null
    [IO.File]::WriteAllText($target, 'old executable fixture')
    [IO.File]::WriteAllText((Join-Path $dataDir 'state.json'), '{"goals":[]}')
    [IO.File]::WriteAllText((Join-Path $dataDir 'endpoint.json'), '{"url":"http://127.0.0.1:12345","token":"fixture-only"}')
    $currentVersion = '0.4.0'
    $repository = 'bytexv/L-Bridge'
    $testState = @{starts=0; stops=0; errors=@(); scenario=$scenario}
    $newBytes = [Text.Encoding]::UTF8.GetBytes(('new fixture '+('x'*2048)))
    $sha = [Security.Cryptography.SHA256]::Create()
    $digest = 'sha256:'+([BitConverter]::ToString($sha.ComputeHash($newBytes))).Replace('-','').ToLowerInvariant()
    $sha.Dispose()
    function Invoke-RestMethod {
      param($Uri,$Headers,$TimeoutSec)
      if ($Uri -like 'https://api.github.com/*') {
        $tag = if ($scenario -eq 'current') {'v0.4.0'} else {'v0.5.0'}
        $d = if ($scenario -eq 'missing-digest') {''} elseif ($scenario -eq 'bad-checksum') {'sha256:'+('0'*64)} else {$digest}
        return [pscustomobject]@{tag_name=$tag;draft=$false;prerelease=$false;assets=@([pscustomobject]@{name='L-Bridge.exe';size=$newBytes.Length;digest=$d;browser_download_url=('https://github.com/'+$repository+'/releases/download/'+$tag+'/L-Bridge.exe')})}
      }
      if ($Uri -like '*/api/state') { return [pscustomobject]@{busy=($scenario -eq 'busy');goals=@()} }
      if ($Uri -like '*/api/health') { return [pscustomobject]@{version=$(if($scenario -eq 'rollback'){'0.4.0'}else{'0.5.0'})} }
      throw 'Unexpected network operation in fixture'
    }
    function Invoke-WebRequest { param([switch]$UseBasicParsing,$Uri,$OutFile,$TimeoutSec) [IO.File]::WriteAllBytes($OutFile,$newBytes) }
    function Start-Process {
      param($FilePath,$ArgumentList,[switch]$Wait,[switch]$PassThru)
      if ($ArgumentList -contains '--stop') { $testState.stops++; return [pscustomobject]@{ExitCode=0} }
      $testState.starts++
      return [pscustomobject]@{Id=999999;HasExited=$false}
    }
    function Stop-Process { param($Id,$ErrorAction) }
    function Start-Sleep { param($Milliseconds) }
    function Read-Host { param($Prompt) }
    function Write-Host { param($Object,$ForegroundColor) $testState.errors += [string]$Object }
    try {
      & $program
      $actual = [IO.File]::ReadAllText($target)
      $expected = if ($scenario -eq 'healthy') {[Text.Encoding]::UTF8.GetString($newBytes)} else {'old executable fixture'}
      if ($actual -cne $expected) { throw "$scenario changed the executable incorrectly" }
      if ([IO.File]::ReadAllText((Join-Path $dataDir 'state.json')) -cne '{"goals":[]}') { throw "$scenario changed saved goals" }
      if ($scenario -in @('current','missing-digest','bad-checksum','busy') -and ($testState.starts -or $testState.stops)) { throw "$scenario stopped or started an app" }
      if ($scenario -eq 'healthy' -and ($testState.starts -ne 1 -or $testState.stops -ne 1)) { throw 'Healthy update did not stop and restart' }
      if ($scenario -eq 'rollback' -and ($testState.starts -ne 2 -or $testState.stops -ne 1)) { throw 'Rollback did not restart the previous executable' }
      if ($scenario -in @('healthy','rollback') -and -not (Test-Path (Join-Path $dataDir 'state.before-update.json'))) { throw 'Missing pre-update backup' }
      if (@(Get-ChildItem $root -Directory -Filter '.lbridge-update-*').Count) { throw 'Staging directory leaked' }
      # The exclusive updater handle must be released on every return path.
      $probe=[IO.File]::Open((Join-Path $root '.lbridge-update.lock'),'Open','ReadWrite','None');$probe.Dispose()
    } finally { Remove-Item -LiteralPath $root -Recurse -Force }
  } $scenario $program
  Write-Output "PASS updater scenario: $scenario"
}
