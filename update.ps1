$ErrorActionPreference = 'Stop'
$stage = $null
$backup = $null
$installed = $false
$stopped = $false
$lock = $null
try {
  $lockPath = Join-Path (Split-Path $target) '.lbridge-update.lock'
  $lock = [IO.File]::Open($lockPath, 'OpenOrCreate', 'ReadWrite', 'None')
  Write-Host 'Checking the official L-Bridge GitHub releases...'
  $release = Invoke-RestMethod -Uri ('https://api.github.com/repos/' + $repository + '/releases/latest') -Headers @{'User-Agent'='L-Bridge-Updater';'Accept'='application/vnd.github+json'} -TimeoutSec 30
  $next = $release.tag_name -replace '^v',''
  if ($next -notmatch '^\d+\.\d+\.\d+$' -or $release.draft -or $release.prerelease) { throw 'The release is not a supported stable version.' }
  if ([version]$next -le [version]$currentVersion) { Write-Host ('Already up to date: '+$currentVersion); return }
  $asset = @($release.assets | Where-Object { $_.name -eq 'L-Bridge.exe' })
  if ($asset.Count -ne 1 -or $asset[0].digest -notmatch '^sha256:[a-fA-F0-9]{64}$') { throw 'This release is missing its EXE or GitHub SHA-256 digest.' }
  $asset = $asset[0]
  if ($asset.size -lt 1024 -or $asset.size -gt 200MB) { throw 'Unexpected release size.' }
  $expectedURL = 'https://github.com/'+$repository+'/releases/download/'+$release.tag_name+'/L-Bridge.exe'
  if ($asset.browser_download_url -cne $expectedURL) { throw 'Unexpected release download address.' }
  $stage = Join-Path (Split-Path $target) ('.lbridge-update-'+[guid]::NewGuid().ToString())
  New-Item -ItemType Directory -Path $stage | Out-Null
  $download = Join-Path $stage 'L-Bridge.exe'
  Invoke-WebRequest -UseBasicParsing -Uri $expectedURL -OutFile $download -TimeoutSec 180
  $actual = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant()
  if (('sha256:'+$actual) -cne $asset.digest.ToLowerInvariant()) { throw 'Download checksum mismatch. The existing version was not changed.' }
  $endpointPath = Join-Path $dataDir 'endpoint.json'
  if (Test-Path -LiteralPath $endpointPath) {
    $ep = Get-Content -LiteralPath $endpointPath -Raw | ConvertFrom-Json
    if ($ep.url -notmatch '^http://127\.0\.0\.1:\d+$') { throw 'Invalid recorded local service address.' }
    $state = Invoke-RestMethod -Uri ($ep.url+'/api/state') -Headers @{Authorization=('Bearer '+$ep.token)} -TimeoutSec 5
    if ($state.busy -or @($state.goals | Where-Object { $_.team.status -eq 'running' }).Count) { throw 'Stop the team and wait for active tools before updating.' }
    $stop = Start-Process -FilePath $target -ArgumentList @('--stop','--data-dir',('"'+$dataDir+'"')) -Wait -PassThru
    if ($stop.ExitCode -ne 0) { throw 'The running bridge could not stop. The existing EXE was not changed.' }
    $stopped = $true
  }
  if (Test-Path -LiteralPath (Join-Path $dataDir 'state.json')) { Copy-Item -LiteralPath (Join-Path $dataDir 'state.json') -Destination (Join-Path $dataDir 'state.before-update.json') -Force }
  $backup = $target+'.previous-'+[guid]::NewGuid().ToString()+'.exe'
  # Running desktop adapters may still hold this EXE open. Give them time to close.
  for ($i=0; $i -lt 40; $i++) {
    try { Move-Item -LiteralPath $target -Destination $backup; break } catch { if ($i -eq 39) { throw 'Close the connected Claude/Codex apps and try again; the EXE is still in use.' }; Start-Sleep -Milliseconds 500 }
  }
  Move-Item -LiteralPath $download -Destination $target
  $installed = $true
  $started = Start-Process -FilePath $target -ArgumentList @('--data-dir',('"'+$dataDir+'"')) -PassThru
  $healthy = $false
  for ($i=0; $i -lt 40; $i++) {
    Start-Sleep -Milliseconds 500
    try {
      $ep = Get-Content -LiteralPath $endpointPath -Raw | ConvertFrom-Json
      if ($ep.url -notmatch '^http://127\.0\.0\.1:\d+$') { continue }
      $health = Invoke-RestMethod -Uri ($ep.url+'/api/health') -Headers @{Authorization=('Bearer '+$ep.token)} -TimeoutSec 2
      if ($health.version -eq $next) { $healthy = $true; break }
    } catch {}
  }
  if (-not $healthy) { throw 'The new version did not become ready. Restoring the previous EXE.' }
  Write-Host ('Updated to '+$next+'. Your saved goals are ready. Use Continue task when ready.') -ForegroundColor Green
  Write-Host ('Previous executable: '+$backup)
} catch {
  Write-Host $_ -ForegroundColor Red
  if ($backup -and (Test-Path -LiteralPath $backup)) {
    if ($installed -and $started -and -not $started.HasExited) { Stop-Process -Id $started.Id -ErrorAction SilentlyContinue }
    try {
      if ($installed -and (Test-Path -LiteralPath $target)) { Remove-Item -LiteralPath $target }
      Move-Item -LiteralPath $backup -Destination $target
      Start-Process -FilePath $target -ArgumentList @('--data-dir',('"'+$dataDir+'"'))
      Write-Host 'Previous executable restored. Saved task data was retained.'
    } catch { Write-Host ('Automatic restore could not finish. Previous EXE is at '+$backup) -ForegroundColor Red }
  } elseif ($stopped) { Start-Process -FilePath $target -ArgumentList @('--data-dir',('"'+$dataDir+'"')) }
} finally {
  if ($stage -and (Test-Path -LiteralPath $stage)) { Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue }
  if ($lock) { $lock.Dispose() }
  Read-Host 'Press Enter to close'
}
