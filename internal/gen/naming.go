package gen

import (
	"strings"
	"unicode"
)

var initialisms = map[string]bool{
	"ACL": true, "API": true, "ASCII": true, "CIDR": true, "CPU": true, "CSS": true,
	"DB": true, "DNS": true, "EOF": true, "GUID": true, "HTML": true, "HTTP": true,
	"HTTPS": true, "IAM": true, "ID": true, "IO": true, "IP": true, "JSON": true,
	"OS": true, "QPS": true, "RAM": true, "RPC": true, "SLA": true, "SMTP": true,
	"SQL": true, "SSH": true, "SSO": true, "TCP": true, "TLS": true, "TTL": true,
	"UDP": true, "UI": true, "UID": true, "URI": true, "URL": true, "UTF8": true,
	"UUID": true, "VM": true, "XML": true, "XSRF": true, "XSS": true,
}

// Terraform meta-arguments that cannot be used as attribute names.
var reservedTF = map[string]bool{
	"connection": true, "count": true, "depends_on": true, "for_each": true,
	"lifecycle": true, "provider": true, "provisioner": true,
}

// words splits an identifier on separators and camel-case boundaries.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(cur) > 0 && unicode.IsUpper(r) {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// GoName returns an exported Go identifier for s: "created_at" -> "CreatedAt",
// "userId" -> "UserID".
func GoName(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		if up := strings.ToUpper(w); initialisms[up] {
			b.WriteString(up)
			continue
		}
		rs := []rune(w)
		b.WriteRune(unicode.ToUpper(rs[0]))
		b.WriteString(string(rs[1:]))
	}
	name := b.String()
	if name == "" {
		return "Field"
	}
	if unicode.IsDigit([]rune(name)[0]) {
		return "X" + name
	}
	return name
}

// TFName returns a Terraform attribute name for s: "createdAt" ->
// "created_at". Meta-argument names get a "_value" suffix.
func TFName(s string) string {
	var parts []string
	for _, w := range words(s) {
		var b strings.Builder
		for _, r := range strings.ToLower(w) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			parts = append(parts, b.String())
		}
	}
	name := strings.Join(parts, "_")
	switch {
	case name == "":
		name = "field"
	case name[0] >= '0' && name[0] <= '9':
		name = "x_" + name
	}
	if reservedTF[name] {
		name += "_value"
	}
	return name
}

// lowerFirst returns the unexported form of an exported Go name:
// "ProjectSettings" -> "projectSettings", "APIKey" -> "apiKey".
func lowerFirst(s string) string {
	rs := []rune(s)
	n := 0
	for n < len(rs) && unicode.IsUpper(rs[n]) {
		n++
	}
	switch {
	case n == 0:
		return s
	case n == len(rs):
		return strings.ToLower(s)
	case n > 1:
		n-- // keep the start of the next word: "APIKey" -> "apiKey"
	}
	for i := 0; i < n; i++ {
		rs[i] = unicode.ToLower(rs[i])
	}
	return string(rs)
}

// FileName returns the snake-case file name stem of a Go name.
func FileName(goName string) string {
	var parts []string
	for _, w := range words(goName) {
		parts = append(parts, strings.ToLower(w))
	}
	return strings.Join(parts, "_")
}

// validJSONTag reports whether name can be used in a json struct tag.
func validJSONTag(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}
