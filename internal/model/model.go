// Package model defines the normalized, format-independent representation of
// an AI coding-agent session. Parsers fill it in strictly from what the export
// records; anything an export does not record stays nil/empty and is reported
// through Support rather than guessed.
package model

import "time"

// Format identifies a supported session export format.
type Format string

const (
	FormatAuto       Format = ""
	FormatClaudeCode Format = "claude-code"
	FormatCodex      Format = "codex"
)

// Formats lists the supported formats in a stable order.
var Formats = []Format{FormatClaudeCode, FormatCodex}

// Op is a file-change operation as recorded by the agent's edit tool.
type Op string

const (
	OpCreate Op = "create"
	OpModify Op = "modify"
	OpDelete Op = "delete"
	OpRename Op = "rename"
	// OpWrite is a whole-file write where the export does not say whether the
	// file existed before (create or overwrite).
	OpWrite Op = "write"
)

// Status is the recorded outcome of a tool call.
type Status string

const (
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusRejected    Status = "rejected"
	StatusInterrupted Status = "interrupted"
	// StatusUnknown means no result was recorded for the call.
	StatusUnknown Status = "unknown"
)

// Session is one normalized session export.
type Session struct {
	Label        string     `json:"label"`
	SourcePath   string     `json:"source_path"`
	Format       Format     `json:"format"`
	SessionIDs   []string   `json:"session_ids"`
	AgentVersion string     `json:"agent_version,omitempty"`
	CWD          string     `json:"cwd,omitempty"`
	GitBranch    string     `json:"git_branch,omitempty"`
	Models       []string   `json:"models"`
	FirstTime    *time.Time `json:"first_timestamp,omitempty"`
	LastTime     *time.Time `json:"last_timestamp,omitempty"`

	Lines            int `json:"lines"`
	Records          int `json:"records"`
	MalformedRecords int `json:"malformed_records"`
	MissingTimes     int `json:"records_without_timestamp"`

	FileChanges []FileChange `json:"file_changes"`
	Commands    []Command    `json:"commands"`
	Tests       []TestRun    `json:"tests"`

	// NotInterpreted counts record types present in the export that this tool
	// does not read (e.g. token usage, reasoning, UI state).
	NotInterpreted []NamedCount `json:"record_types_not_interpreted"`
	// OtherTools counts tool calls that are neither file edits nor shell
	// commands (searches, reads, web fetches...). They are not compared.
	OtherTools []NamedCount `json:"other_tool_calls"`

	Support       []FieldSupport `json:"field_support"`
	Warnings      []Warning      `json:"warnings"`
	WarningsTotal int            `json:"warnings_total"`
}

// FileChange is one recorded change to one file.
type FileChange struct {
	Seq       int        `json:"seq"`
	Line      int        `json:"source_line"`
	Path      string     `json:"path"`
	NewPath   string     `json:"new_path,omitempty"`
	Op        Op         `json:"op"`
	Tool      string     `json:"tool"`
	Status    Status     `json:"status"`
	Timestamp *time.Time `json:"timestamp,omitempty"`
	Added     *int       `json:"lines_added,omitempty"`
	Removed   *int       `json:"lines_removed,omitempty"`
	Order     int        `json:"-"`
}

// Command is one recorded shell command. It is never executed by this tool.
type Command struct {
	Seq        int        `json:"seq"`
	Line       int        `json:"source_line"`
	Text       string     `json:"text"`
	Tool       string     `json:"tool"`
	Status     Status     `json:"status"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	DurationMS *int64     `json:"duration_ms,omitempty"`
	Timestamp  *time.Time `json:"timestamp,omitempty"`
	// TimestampKind says what Timestamp marks: "call" (when the agent issued
	// the command) or "completion" (when the result was recorded).
	TimestampKind string `json:"timestamp_kind,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	TestRunner    string `json:"test_runner,omitempty"`
	// Order is the parser's position key (source line, then block index).
	Order int `json:"-"`
}

// TestRun is a command recognised as a test-runner invocation by its command
// text. Outcome comes from the recorded command status; counts only from a
// summary line present in the recorded output.
type TestRun struct {
	CommandSeq   int        `json:"command_seq"`
	Command      string     `json:"command"`
	Runner       string     `json:"runner"`
	Outcome      Status     `json:"outcome"`
	Passed       *int       `json:"passed,omitempty"`
	Failed       *int       `json:"failed,omitempty"`
	Skipped      *int       `json:"skipped,omitempty"`
	CountsSource string     `json:"counts_source,omitempty"`
	Timestamp    *time.Time `json:"timestamp,omitempty"`
}

// Support levels for FieldSupport.
const (
	SupportRecorded      = "recorded"
	SupportPartial       = "partial"
	SupportNotRecorded   = "not recorded"
	SupportUnsupported   = "unsupported"
	SupportNotApplicable = "n/a"
)

// FieldSupport describes how completely one normalized field is backed by
// the export.
type FieldSupport struct {
	Field    string `json:"field"`
	Level    string `json:"level"`
	Coverage string `json:"coverage,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Warning is a non-fatal parse problem tied to a source line.
type Warning struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// NamedCount is a name with an occurrence count.
type NamedCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// IntPtr and Int64Ptr are small helpers for optional values.
func IntPtr(v int) *int       { return &v }
func Int64Ptr(v int64) *int64 { return &v }
