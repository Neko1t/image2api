param(
  [string]$Base = "https://lunixai.xyz/admin/api",
  [string]$Identifier,
  [securestring]$Password
)

$ErrorActionPreference = "Stop"

if (-not $Identifier) {
  $Identifier = Read-Host "Identifier"
}

if (-not $Password) {
  $Password = Read-Host "Password" -AsSecureString
}

$ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Password)
$plainPassword = $null

try {
  $plainPassword = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)

  $login = Invoke-RestMethod `
    -Method POST `
    -Uri "$Base/auth/login" `
    -ContentType "application/json" `
    -Body (@{
      identifier = $Identifier
      password = $plainPassword
    } | ConvertTo-Json)

  Write-Host "Login OK: $($login.ok)"
  Write-Host "User: $($login.user.email) / role=$($login.user.role)"

  if ($login.token) {
    $prefixLength = [Math]::Min(12, $login.token.Length)
    Write-Host "Token prefix: $($login.token.Substring(0, $prefixLength))..."
  }

  $me = Invoke-RestMethod `
    -Method GET `
    -Uri "$Base/auth/me" `
    -Headers @{ Authorization = "Bearer $($login.token)" }

  Write-Host "Bearer token verified."
  $me | ConvertTo-Json -Depth 8
}
catch {
  Write-Host "Request failed:"
  Write-Host $_.Exception.Message
  if ($_.ErrorDetails.Message) {
    Write-Host $_.ErrorDetails.Message
  }
  exit 1
}
finally {
  if ($ptr) {
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
  }
  $plainPassword = $null
}
