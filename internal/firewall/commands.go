package firewall

import "regexp"

// Порт regex опасных команд из rules/default_policy.yaml (Cerberus).
// BLOCK-паттерны: деструктивные команды, pipe-to-shell, fork-bomb, known-malicious.

var commandPatterns = []*regexp.Regexp{
	// rm -rf в любой комбинации флагов (Go regexp не поддерживает lookahead — перечисляем варианты)
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*|--recursive\s+--force|--force\s+--recursive)\b`),
	// find -delete / -exec rm, shred, dd of=, mkfs, truncate -s0
	regexp.MustCompile(`(?i)\bfind\b[^|;&]*-delete\b|\bfind\b[^|;&]*-exec\s+rm\b|\bshred\b|\bdd\b[^|;&]*\bof=|\bmkfs\b|\btruncate\b[^|;&]*-s\s*0\b`),
	// fork bomb
	regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:`),
	// curl/wget | sh
	regexp.MustCompile(`(?i)(curl|wget|fetch)\b[^|]*\|\s*(sudo\s+)?(ba|z|k|c|da)?sh\b|(base64\s+(-d|--decode)|xxd\s+-r|openssl\s+enc\b[^|]*-d)\b[^|]*\|\s*(sudo\s+)?(ba|z)?sh\b`),
	// pipe to PowerShell iex
	regexp.MustCompile(`(Invoke-WebRequest|iwr|Invoke-RestMethod|irm|DownloadString)\b[^|;]*\|\s*(Invoke-Expression|iex)\b`),
	// known malicious package install
	regexp.MustCompile(`(?i)\b(pip[0-9.]*|pip3|uv|npm|pnpm|yarn|bun|poetry|gem|cargo)\s+(install|add|i)\b[^|;&]*\bgrokwrapper\b|\blitellm\b\s*(==|@)\s*1\.82\.[78]\b|\bxz(-?utils)?\b[^|;&]*\b5\.6\.[01]\b`),
	// known malicious endpoint
	regexp.MustCompile(`(?i)\b(awstore\.cloud|api\.kiro\.cheap|eth-fastscan\.org|jsonkeeper\.com|recargapopular\.com|welovechinatown\.info)\b`),
	// defense evasion: clearing logs, disabling AMSI/Defender
	regexp.MustCompile(`(?i)\bwevtutil(\.exe)?\s+(cl|clear-log)\b|\bClear-EventLog\b|\bClear-History\b|\bauditpol\b[^|;&]*/clear|\bvssadmin\b[^|;&]*delete\s+shadows|\bAmsiUtils\b|\bamsiInitFailed\b|\bSet-MpPreference\b[^|;&]*-Disable|\bAdd-MpPreference\b[^|;&]*ExclusionPath`),
	// Remove-Item -Recurse/-Force, rd /s, del /q
	regexp.MustCompile(`\b(Remove-Item|ri|rmdir|rd)\b[^|;]*-(Recurse|Force)\b|\b(rd|rmdir)\b[^|;]*\s/s\b|\b(del|erase)\b[^|;]*\s/[sq]\b`),
}

// ClassifyCommand возвращает true если в тексте найдена опасная команда.
func ClassifyCommand(text string) bool {
	for _, re := range commandPatterns {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}
