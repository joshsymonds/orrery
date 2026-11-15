// Package redact replaces the credentials in text with a placeholder and leaves every other byte as it was.
package redact

import "regexp"

// placeholder stands in for each redacted credential.
const placeholder = "[redacted]"

var (
	// pemBlock matches a private key block from its BEGIN line to its END line, or to the end of the text when the
	// END line is missing.
	pemBlock = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`)
	// pemTail matches the start of a private key block whose BEGIN line is missing, from the start of the text to
	// its END line.
	pemTail = regexp.MustCompile(`\A[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
	// authHeader matches an Authorization, Proxy-Authorization or x-api-key header line. Group 1 is the header name
	// and group 2 the separator after it.
	authHeader = regexp.MustCompile(`(?i)((?:proxy-)?authorization|x-api-key)([ \t]*:[ \t]*)[^\r\n]*`)
	// awsSecret matches the value of an aws_secret_access_key assignment. Group 1 is the name, the separator and any
	// quote before the value.
	awsSecret = regexp.MustCompile(`(?i)(aws_secret_access_key["']?[ \t]*[=:][ \t]*["']?)[^\s"']+`)
	// skKey matches an sk- key. Group 1 is the character before it, which must not be a letter: a letter before sk-
	// makes an ordinary word such as risk-management, which the key pattern would otherwise rewrite.
	skKey = regexp.MustCompile(`(^|[^A-Za-z])sk-[A-Za-z0-9_-]{20,}`)
	// tokenPattern matches the other prefixed tokens. Each alternative has its prefix and a minimum body length, and
	// none has a leading boundary, so a prefix after an underscore or a letter is still redacted.
	tokenPattern = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}` +
		`|github_pat_[A-Za-z0-9_]{20,}` +
		`|(?:AKIA|ASIA)[0-9A-Z]{16}\b` +
		`|xox[a-z]-[A-Za-z0-9_-]{10,}`)
)

// Text returns text with each credential replaced by [redacted]. Private key blocks are replaced whole, including a
// block whose BEGIN or END line lies outside text. A header value is replaced to the end of its line, and the header
// name stays.
func Text(text string) string {
	text = pemBlock.ReplaceAllLiteralString(text, placeholder)
	text = pemTail.ReplaceAllLiteralString(text, placeholder)
	text = authHeader.ReplaceAllString(text, "${1}${2}"+placeholder)
	text = awsSecret.ReplaceAllString(text, "${1}"+placeholder)
	text = skKey.ReplaceAllString(text, "${1}"+placeholder)
	return tokenPattern.ReplaceAllLiteralString(text, placeholder)
}
