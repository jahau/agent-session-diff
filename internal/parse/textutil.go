package parse

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// commandFromArgv turns a recorded argv into display text. A shell wrapper
// such as ["bash","-lc","<script>"] or ["powershell.exe","-Command","<script>"]
// is reduced to the script, because that is what the agent asked to run.
func commandFromArgv(argv []string) string {
	if len(argv) == 3 {
		sh := strings.ToLower(path.Base(strings.ReplaceAll(argv[0], "\\", "/")))
		sh = strings.TrimSuffix(sh, ".exe")
		flag := strings.ToLower(argv[1])
		switch sh {
		case "bash", "sh", "zsh", "dash", "fish":
			if flag == "-c" || flag == "-lc" || flag == "-cl" {
				return argv[2]
			}
		case "powershell", "pwsh":
			if flag == "-command" || flag == "-c" {
				return argv[2]
			}
		case "cmd":
			if flag == "/c" {
				return argv[2]
			}
		}
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			parts[i] = strconv.Quote(a)
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

// diffCounts counts added/removed lines in a unified diff, ignoring file
// headers.
func diffCounts(diff string) (added, removed int) {
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ ") || strings.HasPrefix(l, "--- "):
		case strings.HasPrefix(l, "+"):
			added++
		case strings.HasPrefix(l, "-"):
			removed++
		}
	}
	return
}

// lineCount counts lines in file content as an editor would.
func lineCount(content string) int {
	if content == "" {
		return 0
	}
	n := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		n++
	}
	return n
}

// patchFile is one file section of a Codex apply_patch envelope.
type patchFile struct {
	op      string // add, update, delete
	path    string
	moveTo  string
	added   int
	removed int
}

// parseApplyPatch reads the "*** Begin Patch" envelope used by Codex.
func parseApplyPatch(input string) []patchFile {
	var files []patchFile
	var cur *patchFile
	for _, l := range strings.Split(input, "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.HasPrefix(l, "*** Add File: "):
			files = append(files, patchFile{op: "add", path: strings.TrimSpace(l[len("*** Add File: "):])})
			cur = &files[len(files)-1]
		case strings.HasPrefix(l, "*** Update File: "):
			files = append(files, patchFile{op: "update", path: strings.TrimSpace(l[len("*** Update File: "):])})
			cur = &files[len(files)-1]
		case strings.HasPrefix(l, "*** Delete File: "):
			files = append(files, patchFile{op: "delete", path: strings.TrimSpace(l[len("*** Delete File: "):])})
			cur = &files[len(files)-1]
		case strings.HasPrefix(l, "*** Move to: "):
			if cur != nil {
				cur.moveTo = strings.TrimSpace(l[len("*** Move to: "):])
			}
		case strings.HasPrefix(l, "***"):
			// Begin/End Patch, End of File markers.
		case cur != nil && strings.HasPrefix(l, "+"):
			cur.added++
		case cur != nil && strings.HasPrefix(l, "-"):
			cur.removed++
		}
	}
	return files
}

var (
	exitCodeLine = regexp.MustCompile(`(?m)^Exit code:? (-?\d+)\b`)
	wallTimeLine = regexp.MustCompile(`(?m)^Wall time:? ([0-9]+(?:\.[0-9]+)?) seconds?`)
)

// legacyExitAndWall reads "Exit code: N" and "Wall time: X seconds" headers
// from legacy Codex shell output text.
func legacyExitAndWall(out string) (exit *int, wallMS *int64) {
	head := out
	if len(head) > 512 {
		head = head[:512]
	}
	if m := exitCodeLine.FindStringSubmatch(head); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			exit = &n
		}
	}
	if m := wallTimeLine.FindStringSubmatch(head); m != nil {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			ms := int64(f*1000 + 0.5)
			wallMS = &ms
		}
	}
	return
}
