//go:build wasip1

package surveyext

import (
	"errors"
	"io"
)

// A WebAssembly build has no way to start an editor: wasip1 has neither fork nor
// exec, so there is no second process to hand the buffer to. Callers that would
// open one are told so plainly instead of failing on a missing binary.
//
// The text itself is not lost: every caller of Edit takes the body from a flag
// (--body, --body-file) when one is given, and the wasip1 prompter reads a
// multi-line body from stdin (see internal/prompter).

var errNoEditor = errors.New("gh cannot open an editor in the browser: pass the text with --body or --body-file")

func Edit(_, _, _ string, _ io.Reader, _ io.Writer, _ io.Writer) (string, error) {
	return "", errNoEditor
}

// EditorName is what gh prints when it offers to launch an editor.
func EditorName(string) string { return "an editor" }
