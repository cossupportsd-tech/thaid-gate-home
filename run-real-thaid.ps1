# Runs the website against the real ThaID (production) for a test login.
# Credentials are read from the text file DOPA issued, so the secret is never
# stored in this project. Usage:
#   .\run-real-thaid.ps1 -CredFile "C:\path\to\credentials.txt"
#
# By default ThaID sends the browser straight back to this website
# (http://localhost:8081/callback). Use -UseRegisteredCallback to send it to
# the Callback Endpoint in the file instead and paste the URL back by hand.
param(
    [Parameter(Mandatory = $true)][string]$CredFile,
    [string]$RedirectUrl = "http://localhost:8081/callback",
    [switch]$UseRegisteredCallback
)

$lines = Get-Content -LiteralPath $CredFile -Encoding UTF8
function Field($name) {
    $line = $lines | Where-Object { $_ -match "^\s*$name\s*:" } | Select-Object -First 1
    if (-not $line) { throw "Field '$name' not found in $CredFile" }
    return ($line -replace "^\s*$name\s*:\s*", "").Trim()
}

$env:THAID_ISSUER = "https://imauth.bora.dopa.go.th"
$env:THAID_CLIENT_ID = Field "Client ID"
$env:THAID_CLIENT_SECRET = Field "Client Secret"
# Scopes this registration is approved for (others are refused with "Bad data request").
$env:THAID_SCOPES = "openid pid title given_name family_name gender"
$env:THAID_USE_PKCE = "true"
# ThaID returns the nonce (seen in a real login), so require it.
$env:THAID_ALLOW_MISSING_NONCE = "false"
$env:LISTEN = "127.0.0.1:8081"
if ($UseRegisteredCallback) {
    $env:THAID_REDIRECT_URL = Field "Callback Endpoint"
    $env:THAID_PASTE_CALLBACK = "true"
} else {
    $env:THAID_REDIRECT_URL = $RedirectUrl
    $env:THAID_PASTE_CALLBACK = "false"
}

Set-Location -LiteralPath $PSScriptRoot
go run ./cmd/webapp
