// Package redact removes secrets and local paths from normalized sessions
// before they are compared or rendered. Redaction is deterministic: the same
// input always produces the same placeholder, so redacted sessions still
// compare correctly against each other.
package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/jahau/agent-session-diff/internal/model"
)

// Options selects what to redact.
type Options struct {
	Secrets bool
	Paths   bool
}

// Enabled reports whether any redaction is requested.
func (o Options) Enabled() bool { return o.Secrets || o.Paths }

type secretRule struct {
	name string
	re   *regexp.Regexp
	// group is the submatch index holding the secret; 0 means whole match.
	group int
}

// Patterns are deliberately conservative token shapes plus key=value forms.
var secretRules = []secretRule{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z ]*PRIVATE KEY-----|$)`), 0},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{16,}`), 0},
	{"openai-key", regexp.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_\-]{20,}`), 0},
	{"github-token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`), 0},
	{"gitlab-token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}`), 0},
	{"slack-token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9\-]{10,}`), 0},
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`), 0},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}`), 0},
	{"stripe-key", regexp.MustCompile(`\b[rs]k_(?:live|test)_[A-Za-z0-9]{16,}`), 0},
	{"npm-token", regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`), 0},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`), 0},
	{"url-credentials", regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://[^\s/:@"']+:([^\s/@"']+)@`), 1},
	{"bearer-token", regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9._~+/\-]{16,}=*)`), 1},
	{"assignment", regexp.MustCompile(`(?i)\b[A-Z0-9_\-]*(?:password|passwd|pwd|secret|token|api[_\-]?key|access[_\-]?key|private[_\-]?key|client[_\-]?secret)[A-Z0-9_\-]*["']?\s*[:=]\s*("[^"\s]{4,}"|'[^'\s]{4,}'|[^\s"'&;|]{4,})`), 1},
	{"cli-secret-flag", regexp.MustCompile(`(?i)--(?:password|token|api-key|secret)(?:=|\s+)("[^"]+"|'[^']+'|\S+)`), 1},
}

// Secrets replaces secret-looking substrings with "[REDACTED:<kind>]".
func Secrets(s string) string {
	for _, r := range secretRules {
		if r.group == 0 {
			s = r.re.ReplaceAllString(s, "[REDACTED:"+r.name+"]")
			continue
		}
		s = r.re.ReplaceAllStringFunc(s, func(m string) string {
			sub := r.re.FindStringSubmatchIndex(m)
			if sub == nil || sub[2*r.group] < 0 {
				return m
			}
			val := m[sub[2*r.group]:sub[2*r.group+1]]
			if strings.HasPrefix(val, "[REDACTED:") || strings.HasPrefix(val, "$") {
				return m // already redacted, or a variable reference, not a value
			}
			return m[:sub[2*r.group]] + "[REDACTED:" + r.name + "]" + m[sub[2*r.group+1]:]
		})
	}
	return s
}

var (
	winHome  = regexp.MustCompile(`(?i)\b[A-Z]:[\\/]+Users[\\/]+[^\\/\s"'<>|]+`)
	unixHome = regexp.MustCompile(`(?:^|[\s"'=(])(/(?:home|Users)/[^/\s"'<>|]+|/root)(?:[/\s"')]|$)`)
	winAbs   = regexp.MustCompile(`\b[A-Za-z]:[\\/][^\s"'<>|*?]*`)
	// At least two segments, so switches such as "cmd /c" are left alone.
	unixAbs  = regexp.MustCompile(`(^|[\s"'=(])(/[A-Za-z0-9._\-]+/[^\s"'<>|]*)`)
	tildeAbs = regexp.MustCompile(`<home>[\\/][^\s"'<>|]*`)
)

// Paths rewrites local paths: the session cwd becomes "<cwd>", user home
// directories become "<home>", and any other absolute path becomes
// "<path:hash>" (a stable hash, so equal paths stay equal).
type Paths struct {
	cwds []string
}

// NewPaths builds a path redactor for the given session working directories.
func NewPaths(cwds ...string) *Paths {
	p := &Paths{}
	for _, c := range cwds {
		if c == "" {
			continue
		}
		c = strings.TrimRight(c, `\/`)
		slash := strings.ReplaceAll(c, `\`, "/")
		p.cwds = append(p.cwds, c, slash, strings.ReplaceAll(c, "/", `\`))
		if len(slash) >= 2 && slash[1] == ':' { // MSYS/Git Bash spelling: /c/...
			p.cwds = append(p.cwds, "/"+strings.ToLower(slash[:1])+slash[2:])
		}
	}
	// Longest first, so a nested cwd is not half-replaced by its parent.
	sort.Slice(p.cwds, func(i, j int) bool {
		if len(p.cwds[i]) != len(p.cwds[j]) {
			return len(p.cwds[i]) > len(p.cwds[j])
		}
		return p.cwds[i] < p.cwds[j]
	})
	return p
}

// String redacts paths in s.
func (p *Paths) String(s string) string {
	for _, c := range p.cwds {
		if c != "" && c != "/" {
			s = replaceFold(s, c, "<cwd>")
		}
	}
	s = winHome.ReplaceAllString(s, "<home>")
	s = unixHome.ReplaceAllStringFunc(s, func(m string) string {
		sub := unixHome.FindStringSubmatchIndex(m)
		return m[:sub[2]] + "<home>" + m[sub[3]:]
	})
	s = tildeAbs.ReplaceAllStringFunc(s, func(m string) string { return "<home>/" + hashed(m[len("<home>")+1:]) })
	s = winAbs.ReplaceAllStringFunc(s, hashedPath)
	s = unixAbs.ReplaceAllStringFunc(s, func(m string) string {
		sub := unixAbs.FindStringSubmatchIndex(m)
		val := m[sub[4]:sub[5]]
		if val == "/" || isHarmlessUnixPath(val) {
			return m
		}
		return m[:sub[4]] + hashedPath(val)
	})
	return s
}

// isHarmlessUnixPath keeps system paths that identify no user or project.
func isHarmlessUnixPath(p string) bool {
	for _, pre := range []string{"/dev/null", "/dev/stdout", "/dev/stderr", "/usr/bin/", "/bin/", "/usr/local/bin/", "/tmp"} {
		if p == strings.TrimSuffix(pre, "/") || strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func hashedPath(p string) string {
	norm := strings.ReplaceAll(p, `\`, "/")
	return "<path:" + hashed(norm) + ">"
}

func hashed(s string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(path.Clean(strings.ReplaceAll(s, `\`, "/")))))
	return hex.EncodeToString(sum[:4])
}

// replaceFold replaces old with new, matching case-insensitively.
func replaceFold(s, old, repl string) string {
	if old == "" {
		return s
	}
	lo, lold := strings.ToLower(s), strings.ToLower(old)
	if len(lo) != len(s) { // non-ASCII case mapping changed lengths; fall back
		return strings.ReplaceAll(s, old, repl)
	}
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(lo[i:], lold)
		if j < 0 {
			b.WriteString(s[i:])
			return b.String()
		}
		b.WriteString(s[i : i+j])
		b.WriteString(repl)
		i += j + len(old)
	}
}

// Session applies redaction to every user-visible string in a session.
// cwds should include the working directories of all compared sessions so a
// path from one session is redacted the same way in the other.
func Session(s *model.Session, o Options, cwds []string) {
	if !o.Enabled() {
		return
	}
	pr := NewPaths(cwds...)
	f := func(v string) string {
		if o.Secrets {
			v = Secrets(v)
		}
		if o.Paths {
			v = pr.String(v)
		}
		return v
	}
	if o.Paths {
		s.SourcePath = path.Base(strings.ReplaceAll(s.SourcePath, `\`, "/"))
		s.Label = path.Base(strings.ReplaceAll(s.Label, `\`, "/"))
		if s.CWD != "" {
			s.CWD = "<cwd>"
		}
	}
	s.Label = f(s.Label)
	s.GitBranch = f(s.GitBranch)
	for i := range s.Commands {
		c := &s.Commands[i]
		c.Text = f(c.Text)
		if c.CWD != "" {
			c.CWD = f(c.CWD)
		}
	}
	for i := range s.Tests {
		s.Tests[i].Command = f(s.Tests[i].Command)
	}
	for i := range s.FileChanges {
		fc := &s.FileChanges[i]
		fc.Path = f(fc.Path)
		fc.NewPath = f(fc.NewPath)
	}
	for i := range s.Warnings {
		s.Warnings[i].Message = f(s.Warnings[i].Message)
	}
}
