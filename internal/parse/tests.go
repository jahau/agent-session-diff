package parse

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/jahau/agent-session-diff/internal/model"
)

// testRunner returns a runner name when any segment of the command text starts
// a known test runner, else "". It only classifies recorded text.
func testRunner(cmd string) string {
	for _, seg := range splitSegments(cmd) {
		if r := segmentRunner(strings.Fields(seg)); r != "" {
			return r
		}
	}
	return ""
}

// splitSegments splits shell text on ;, |, &&, || and newlines that are not
// inside single or double quotes.
func splitSegments(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ';' || r == '|' || r == '\n' || r == '&':
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	return append(out, cur.String())
}

func segmentRunner(tok []string) string {
	for i := range tok {
		tok[i] = strings.Trim(tok[i], `"'`)
	}
	// Skip env assignments and transparent wrappers.
	for len(tok) > 0 {
		t := tok[0]
		switch {
		case strings.Contains(t, "=") && !strings.HasPrefix(t, "-"):
			tok = tok[1:]
		case t == "sudo" || t == "time" || t == "env" || t == "npx" || t == "bunx":
			tok = tok[1:]
		case (t == "uv" || t == "poetry" || t == "pipenv" || t == "pdm" || t == "hatch") && len(tok) > 1 && tok[1] == "run":
			tok = tok[2:]
		case t == "bundle" && len(tok) > 1 && tok[1] == "exec":
			tok = tok[2:]
		default:
			goto done
		}
	}
done:
	if len(tok) == 0 {
		return ""
	}
	first := strings.ToLower(path.Base(strings.ReplaceAll(tok[0], "\\", "/")))
	first = strings.TrimSuffix(first, ".exe")
	second := ""
	if len(tok) > 1 {
		second = strings.ToLower(tok[1])
	}
	third := ""
	if len(tok) > 2 {
		third = strings.ToLower(tok[2])
	}
	switch first {
	case "go":
		if second == "test" {
			return "go test"
		}
	case "pytest", "py.test":
		return "pytest"
	case "python", "python3", "py":
		if second == "-m" && (third == "pytest" || third == "unittest") {
			return third
		}
	case "npm", "pnpm", "yarn", "bun":
		if second == "test" || second == "t" || (second == "run" && strings.HasPrefix(third, "test")) {
			return first + " test"
		}
	case "jest", "vitest", "mocha", "rspec", "phpunit", "ctest", "tox", "nox":
		return first
	case "cargo":
		if second == "test" || second == "nextest" {
			return "cargo test"
		}
	case "mvn", "mvnw":
		if second == "test" || second == "verify" {
			return "maven"
		}
	case "gradle", "gradlew":
		if second == "test" || second == "check" {
			return "gradle"
		}
	case "dotnet", "deno", "mix", "swift":
		if second == "test" {
			return first + " test"
		}
	case "make":
		if second == "test" || second == "check" {
			return "make " + second
		}
	}
	return ""
}

var (
	goPkgOK     = regexp.MustCompile(`(?m)^ok\s+\S+`)
	goPkgFail   = regexp.MustCompile(`(?m)^FAIL\s+\S+`)
	goTestPass  = regexp.MustCompile(`(?m)^\s*--- PASS:`)
	goTestFail  = regexp.MustCompile(`(?m)^\s*--- FAIL:`)
	goTestSkip  = regexp.MustCompile(`(?m)^\s*--- SKIP:`)
	pyCount     = regexp.MustCompile(`\b(\d+) (passed|failed|skipped|errors?)\b`)
	jestLine    = regexp.MustCompile(`(?m)^\s*Tests:?\s+(.*)$`)
	jsCount     = regexp.MustCompile(`\b(\d+) (passed|failed|skipped|todo)\b`)
	cargoResult = regexp.MustCompile(`test result: \w+\. (\d+) passed; (\d+) failed; (\d+) ignored`)
)

// testCounts reads pass/fail/skip counts from a runner summary present in the
// recorded output. ok=false means no recognised summary was found.
func testCounts(runner, out string) (passed, failed, skipped *int, source string, ok bool) {
	if out == "" {
		return nil, nil, nil, "", false
	}
	switch {
	case runner == "go test":
		p, f, s := len(goTestPass.FindAllString(out, -1)), len(goTestFail.FindAllString(out, -1)), len(goTestSkip.FindAllString(out, -1))
		if p+f+s > 0 {
			return model.IntPtr(p), model.IntPtr(f), model.IntPtr(s), "go test -v result lines (tests)", true
		}
		po, pf := len(goPkgOK.FindAllString(out, -1)), len(goPkgFail.FindAllString(out, -1))
		if po+pf > 0 {
			return model.IntPtr(po), model.IntPtr(pf), nil, "go test ok/FAIL lines (packages)", true
		}
	case runner == "pytest":
		lines := strings.Split(out, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			l := lines[i]
			if !strings.Contains(l, " in ") || !(strings.Contains(l, "passed") || strings.Contains(l, "failed") || strings.Contains(l, "error")) {
				continue
			}
			m := pyCount.FindAllStringSubmatch(l, -1)
			if len(m) == 0 {
				continue
			}
			p, f, s := 0, 0, 0
			for _, g := range m {
				n, _ := strconv.Atoi(g[1])
				switch g[2] {
				case "passed":
					p += n
				case "skipped":
					s += n
				default: // failed, error(s)
					f += n
				}
			}
			return model.IntPtr(p), model.IntPtr(f), model.IntPtr(s), "pytest summary line (errors counted as failed)", true
		}
	case runner == "jest" || runner == "vitest" || strings.HasSuffix(runner, " test") && (strings.HasPrefix(runner, "npm") || strings.HasPrefix(runner, "pnpm") || strings.HasPrefix(runner, "yarn") || strings.HasPrefix(runner, "bun")):
		ms := jestLine.FindAllStringSubmatch(out, -1)
		if len(ms) == 0 {
			return nil, nil, nil, "", false
		}
		last := ms[len(ms)-1][1]
		m := jsCount.FindAllStringSubmatch(last, -1)
		if len(m) == 0 {
			return nil, nil, nil, "", false
		}
		p, f, s := 0, 0, 0
		for _, g := range m {
			n, _ := strconv.Atoi(g[1])
			switch g[2] {
			case "passed":
				p += n
			case "failed":
				f += n
			default:
				s += n
			}
		}
		return model.IntPtr(p), model.IntPtr(f), model.IntPtr(s), "jest/vitest \"Tests:\" summary line", true
	case runner == "cargo test":
		ms := cargoResult.FindAllStringSubmatch(out, -1)
		if len(ms) == 0 {
			return nil, nil, nil, "", false
		}
		p, f, s := 0, 0, 0
		for _, g := range ms {
			a, _ := strconv.Atoi(g[1])
			b, _ := strconv.Atoi(g[2])
			c, _ := strconv.Atoi(g[3])
			p, f, s = p+a, f+b, s+c
		}
		return model.IntPtr(p), model.IntPtr(f), model.IntPtr(s), "cargo \"test result\" lines (summed)", true
	}
	return nil, nil, nil, "", false
}
