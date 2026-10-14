# Builds dist\EasyGit.exe (needs Go; rsrc is optional and only adds icon + manifest).
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
go run ./tools/genicon
$rsrc = Join-Path (go env GOPATH) 'bin\rsrc.exe'
if (-not (Test-Path $rsrc)) { go install github.com/akavel/rsrc@latest }
& $rsrc -manifest easygit.manifest -ico easygit.ico -o rsrc_windows_amd64.syso
New-Item -ItemType Directory -Force dist | Out-Null
go build -trimpath -ldflags "-H=windowsgui -s -w" -o dist\EasyGit.exe .
Get-Item dist\EasyGit.exe | Select-Object Name, @{n='MB';e={[math]::Round($_.Length/1MB,2)}}
