#!/usr/bin/env pwsh
# Copyright 2021 the Fay authors. All rights reserved.
# TODO(everyone): Keep this script simple and easily auditable.

$ErrorActionPreference = 'Stop'

$FarosInstall = $env:FarosCTL_INSTALL
$BinDir = if ($FarosInstall) {
  "$FarosInstall\bin"
} else {
  "$Home\.faros\bin"
}

$FarosCtlExe = "$BinDir\faros.exe"

New-Item -ErrorAction Ignore -Force $BinDir -ItemType Directory  | Out-Null

Invoke-WebRequest "https://downloads.faros.sh/cli/windows-amd64.exe" -UseBasicParsing -OutFile $FarosCtlExe

Move-Item $FarosCtlExe -Destination $BinDir

$User = [EnvironmentVariableTarget]::User
$Path = [Environment]::GetEnvironmentVariable('Path', $User)
if (!(";$Path;".ToLower() -like "*;$BinDir;*".ToLower())) {
  [Environment]::SetEnvironmentVariable('Path', "$Path;$BinDir", $User)
  $Env:Path += ";$BinDir"
}

Start-Process -FilePath "$env:comspec" -ArgumentList "/c", "mklink", $FarosCtlExe

Write-Output "faros was installed successfully to $FarosCtlExe"
Write-Output "Run 'faros --help' to get started"
