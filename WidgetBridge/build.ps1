$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path

Push-Location $repoRoot
try {
	$buildArgs = @(
		"build"
		"-ldflags=-H=windowsgui"
		"-o"
		"widgetbridge.exe"
		"."
	)

	& go @buildArgs
}
finally {
	Pop-Location
}