# metiq installer for Windows (PowerShell)
# Usage: iwr -useb https://metiq.dev/install.ps1 | iex
# Or:    & ([scriptblock]::Create((iwr -useb https://metiq.dev/install.ps1))) -Tag v1.0.0

param(
    [string]$Tag       = "latest",
    [string]$Prefix    = "",
    [switch]$NoOnboard,
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"

# ── Colours ───────────────────────────────────────────────────────────────────
$ACCENT  = "`e[38;2;100;180;255m"   # nostr-blue
$SUCCESS = "`e[38;2;0;229;180m"     # teal
$WARN    = "`e[38;2;255;176;32m"    # amber
$ERR     = "`e[38;2;230;57;70m"     # red
$MUTED   = "`e[38;2;90;100;128m"    # dim
$NC      = "`e[0m"

function Write-Info    { param([string]$m) Microsoft.PowerShell.Utility\Write-Host "${MUTED}·${NC} $m" }
function Write-Success { param([string]$m) Microsoft.PowerShell.Utility\Write-Host "${SUCCESS}✓${NC} $m" }
function Write-Warn    { param([string]$m) Microsoft.PowerShell.Utility\Write-Host "${WARN}!${NC} $m" }
function Write-Err     { param([string]$m) Microsoft.PowerShell.Utility\Write-Host "${ERR}✗${NC} $m" }

function Write-Banner {
    Microsoft.PowerShell.Utility\Write-Host ""
    Microsoft.PowerShell.Utility\Write-Host "${ACCENT}  ⚡ metiq installer${NC}"
    Microsoft.PowerShell.Utility\Write-Host "${MUTED}  Nostr-native AI agent daemon.${NC}"
    Microsoft.PowerShell.Utility\Write-Host ""
}

# ── Constants ─────────────────────────────────────────────────────────────────
$GitHubRepo = if ($env:GITHUB_REPO) { $env:GITHUB_REPO } else { "metiq/metiq" }
$ConfigDir  = Join-Path $env:USERPROFILE ".metiq"
$EnvFile    = Join-Path $ConfigDir ".env"

Write-Banner

# ── Platform detection ────────────────────────────────────────────────────────
$Arch = $env:PROCESSOR_ARCHITECTURE
$GoArch = switch ($Arch) {
    "AMD64"   { "amd64" }
    "ARM64"   { "arm64" }
    default   { Write-Err "Unsupported architecture: $Arch"; exit 1 }
}
Write-Success "Detected: windows/$GoArch"

# ── Resolve install location ──────────────────────────────────────────────────
if ([string]::IsNullOrEmpty($Prefix)) {
    $Prefix = Join-Path $env:USERPROFILE ".local"
}
$BinDir = Join-Path $Prefix "bin"

# ── Resolve download URL ──────────────────────────────────────────────────────
function Get-DownloadUrl {
    param([string]$tag)
    if ($tag -eq "latest") {
        $apiUrl  = "https://api.github.com/repos/$GitHubRepo/releases/latest"
        $headers = @{ "User-Agent" = "metiq-installer" }
        try {
            $resp = Invoke-RestMethod -Uri $apiUrl -Headers $headers
            $tag  = $resp.tag_name
        } catch {
            Write-Err "Could not determine latest release: $_"
            exit 1
        }
        Write-Info "Resolved latest tag: $tag"
    }
    return "https://github.com/$GitHubRepo/releases/download/$tag/metiqd-windows-$GoArch.exe"
}

$DownloadUrl = Get-DownloadUrl -tag $Tag
Write-Info "Download URL: $DownloadUrl"

# ── Download binary ───────────────────────────────────────────────────────────
$TmpBin = Join-Path $env:TEMP "metiqd-install.exe"

if ($DryRun) {
    Write-Info "[DRY RUN] Would download: $DownloadUrl -> $BinDir\metiqd.exe"
} else {
    Write-Info "Downloading metiqd ..."
    try {
        Invoke-WebRequest -Uri $DownloadUrl -OutFile $TmpBin -UseBasicParsing
        Write-Success "Downloaded"
    } catch {
        Write-Err "Download failed: $_"
        exit 1
    }
}

# ── Install binary ────────────────────────────────────────────────────────────
$Dest = Join-Path $BinDir "metiqd.exe"
if ($DryRun) {
    Write-Info "[DRY RUN] Would install to: $Dest"
} else {
    if (!(Test-Path $BinDir)) { New-Item -ItemType Directory -Path $BinDir -Force | Out-Null }
    Move-Item -Force $TmpBin $Dest
    Write-Success "Installed to $Dest"
}

# ── Create config dir and .env ────────────────────────────────────────────────
if (!$DryRun) {
    if (!(Test-Path $ConfigDir)) {
        New-Item -ItemType Directory -Path $ConfigDir -Force | Out-Null
        Write-Success "Created config dir: $ConfigDir"
    }
    if (!(Test-Path $EnvFile)) {
        @"
# metiq environment configuration
# Edit this file and fill in the values you need.

# ── Nostr ─────────────────────────────────────────────────────────────────────
# Your agent's Nostr private key (hex or nsec bech32)
#METIQ_NOSTR_KEY=

# Optional: comma-separated relay URLs
#METIQ_NOSTR_RELAYS=wss://nos.lol,wss://relay.primal.net,wss://relay.sharegap.net

# ── LLM providers ─────────────────────────────────────────────────────────────
#ANTHROPIC_API_KEY=
#OPENAI_API_KEY=
#GEMINI_API_KEY=
#XAI_API_KEY=
#GROQ_API_KEY=
#MISTRAL_API_KEY=
#OPENROUTER_API_KEY=
#TOGETHER_API_KEY=

# Default model (e.g. claude-sonnet-4-5, gpt-4o, gemini-2.0-flash, grok-3)
#METIQ_DEFAULT_MODEL=claude-sonnet-4-5

# ── Browser sandbox (optional) ────────────────────────────────────────────────
#METIQ_BROWSER_URL=http://localhost:3500

# ── Skills ───────────────────────────────────────────────────────────────────
#METIQ_MANAGED_SKILLS_DIR=%USERPROFILE%\.metiq\skills
"@ | Set-Content -Path $EnvFile -Encoding UTF8
        Write-Success "Created $EnvFile"
    }
}

# ── Add BinDir to user PATH ───────────────────────────────────────────────────
function Add-ToUserPath {
    param([string]$Dir)
    $currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($currentPath -notlike "*$Dir*") {
        [Environment]::SetEnvironmentVariable("Path", "$currentPath;$Dir", "User")
        Write-Info "Added $Dir to user PATH (restart shell to take effect)"
    }
}

if (!$DryRun) {
    Add-ToUserPath -Dir $BinDir
}

# ── Done ──────────────────────────────────────────────────────────────────────
Microsoft.PowerShell.Utility\Write-Host ""
Microsoft.PowerShell.Utility\Write-Host "${ACCENT}⚡ metiq installed!${NC}"
Microsoft.PowerShell.Utility\Write-Host ""
Write-Info "Config:  $EnvFile"
Write-Info "Binary:  $Dest"
Microsoft.PowerShell.Utility\Write-Host ""
Write-Info "Next steps:"
Write-Info "  1. Edit $EnvFile with your Nostr key + API keys"
Write-Info "  2. Run:  metiqd"
Write-Info "  3. Send a DM on Nostr to your agent's pubkey"
Microsoft.PowerShell.Utility\Write-Host ""
