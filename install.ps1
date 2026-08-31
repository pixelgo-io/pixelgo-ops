# pixelgo-ops - Windows install
#
# SKELETON - untested.

$ErrorActionPreference = "Stop"

$binary  = "pixelgo-ops.exe"
$destDir = "$env:LOCALAPPDATA\pixelgo-ops"

Write-Host "Building..."
go build -o $binary .\cmd\pixelgo-ops

New-Item -ItemType Directory -Force -Path $destDir | Out-Null
Copy-Item $binary -Destination $destDir -Force

# TODO: add $destDir to PATH if not already present
Write-Host "Installed to $destDir"
Write-Host "Add the directory to PATH if it isn't there already."
