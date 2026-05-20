package reflector

import "regexp"

const Redacted = "<redacted>"

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\bA(?:KI|SI)A[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,255}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,255}\b`),
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,255}\b`),
	regexp.MustCompile(`\bsk-proj-[A-Za-z0-9_-]{16,255}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,255}\b`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY|KEY)[A-Z0-9_]*\s*=\s*)(['"]?)[^\s'"` + "`" + `]+(['"]?)`),
	regexp.MustCompile(`(?i)(["']?[A-Za-z0-9_-]*(?:api[_-]?key|auth[_-]?token|token|secret|password|authorization)[A-Za-z0-9_-]*["']?\s*[:=]\s*)(["']?)[^\s"',}]+(["']?)`),
	regexp.MustCompile(`(?i)(//[^\s:]+/:_authToken\s*=\s*)(["']?)[^\s"']+(["']?)`),
}

func Redact(value string) string {
	redacted := value
	for _, pattern := range redactionPatterns {
		if pattern.NumSubexp() >= 3 {
			redacted = pattern.ReplaceAllString(redacted, "${1}${2}"+Redacted+"${3}")
			continue
		}
		redacted = pattern.ReplaceAllString(redacted, Redacted)
	}
	return redacted
}
