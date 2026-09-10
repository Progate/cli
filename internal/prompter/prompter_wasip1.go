//go:build wasip1

package prompter

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cli/cli/v2/internal/ghinstance"
	"github.com/cli/cli/v2/pkg/iostreams"
)

// The prompter a WebAssembly build uses: one question per line, answered by a
// line of input.
//
// # Why not the terminal prompters
//
// The three prompters gh normally picks between (survey, huh, and the
// accessible one) all drive a terminal directly: raw mode, cursor movement,
// resize signals. wasip1 has none of that. The ABI has no ioctl, so there is no
// way to turn off echo or read a key at a time, and the terminal on the other
// side is a canvas in a browser tab.
//
// What is left still works, because it is the oldest interface there is: print
// the question, read a line. Every prompt below is written that way, and gh's
// non-interactive flags (--yes, --title, --body, ...) remain the way to skip
// them entirely.
//
// # What the user loses
//
//   - Password and AuthToken echo what is typed. There is no way to hide it, so
//     the prompt says so rather than pretending.
//   - Selecting is done by number, not with the arrow keys.
//   - MarkdownEditor reads lines until a line containing only ".", since there
//     is no editor to launch (see pkg/surveyext).
type linePrompter struct {
	in  *bufio.Reader
	out io.Writer
}

func New(_ string, io *iostreams.IOStreams) Prompter {
	return &linePrompter{in: bufio.NewReader(io.In), out: io.Out}
}

// ErrEOF is returned when input ends before the question was answered. Commands
// treat it the way they treat a cancelled prompt.
var ErrEOF = errors.New("no answer: input ended")

func (p *linePrompter) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line), nil
		}
		if errors.Is(err, io.EOF) {
			return "", ErrEOF
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (p *linePrompter) ask(question string) (string, error) {
	fmt.Fprint(p.out, question)
	return p.readLine()
}

func (p *linePrompter) Select(prompt, defaultValue string, options []string) (int, error) {
	def := -1
	fmt.Fprintf(p.out, "%s\n", prompt)
	for i, option := range options {
		marker := " "
		if option == defaultValue {
			def = i
			marker = "*"
		}
		fmt.Fprintf(p.out, " %s %d) %s\n", marker, i+1, option)
	}

	for {
		hint := "Choose a number: "
		if def >= 0 {
			hint = fmt.Sprintf("Choose a number [%d]: ", def+1)
		}
		answer, err := p.ask(hint)
		if err != nil {
			return 0, err
		}
		if answer == "" && def >= 0 {
			return def, nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(answer))
		if err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		fmt.Fprintf(p.out, "Enter a number between 1 and %d.\n", len(options))
	}
}

func (p *linePrompter) MultiSelect(prompt string, defaults []string, options []string) ([]int, error) {
	fmt.Fprintf(p.out, "%s\n", prompt)
	var defaultNumbers []string
	for i, option := range options {
		marker := " "
		for _, d := range defaults {
			if d == option {
				marker = "*"
				defaultNumbers = append(defaultNumbers, strconv.Itoa(i+1))
			}
		}
		fmt.Fprintf(p.out, " %s %d) %s\n", marker, i+1, option)
	}

	for {
		hint := "Choose numbers, comma separated (empty for none): "
		if len(defaultNumbers) > 0 {
			hint = fmt.Sprintf("Choose numbers, comma separated [%s]: ", strings.Join(defaultNumbers, ","))
		}
		answer, err := p.ask(hint)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(answer) == "" {
			// An empty answer keeps the defaults, the way the terminal prompters do
			// when the user presses enter without touching anything.
			chosen := make([]int, 0, len(defaultNumbers))
			for i, option := range options {
				if slicesContains(defaults, option) {
					chosen = append(chosen, i)
				}
			}
			return chosen, nil
		}

		chosen, ok := parseNumbers(answer, len(options))
		if ok {
			return chosen, nil
		}
		fmt.Fprintf(p.out, "Enter numbers between 1 and %d, separated by commas.\n", len(options))
	}
}

// parseNumbers turns "1, 3" into []int{0, 2}. Duplicates collapse; anything out
// of range rejects the whole answer, so a typo is not silently dropped.
func parseNumbers(answer string, count int) ([]int, bool) {
	var chosen []int
	for _, field := range strings.Split(answer, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil || n < 1 || n > count {
			return nil, false
		}
		if !slicesContainsInt(chosen, n-1) {
			chosen = append(chosen, n-1)
		}
	}
	return chosen, true
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func slicesContainsInt(haystack []int, needle int) bool {
	for _, n := range haystack {
		if n == needle {
			return true
		}
	}
	return false
}

func (p *linePrompter) MultiSelectWithSearch(prompt, searchPrompt string, defaults []string, persistentOptions []string, searchFunc func(string) MultiSelectSearchResult) ([]string, error) {
	return multiSelectWithSearch(p, prompt, searchPrompt, defaults, persistentOptions, searchFunc)
}

func (p *linePrompter) Input(prompt, defaultValue string) (string, error) {
	question := fmt.Sprintf("%s ", prompt)
	if defaultValue != "" {
		question = fmt.Sprintf("%s [%s] ", prompt, defaultValue)
	}
	answer, err := p.ask(question)
	if err != nil {
		return "", err
	}
	if answer == "" {
		return defaultValue, nil
	}
	return answer, nil
}

func (p *linePrompter) Password(prompt string) (string, error) {
	// Saying this out loud matters: without ioctl the guest cannot turn echo off,
	// so what is typed stays on screen and in the terminal's scrollback.
	fmt.Fprintf(p.out, "%s (typing is visible) ", prompt)
	return p.readLine()
}

func (p *linePrompter) Confirm(prompt string, defaultValue bool) (bool, error) {
	hint := "y/N"
	if defaultValue {
		hint = "Y/n"
	}
	for {
		answer, err := p.ask(fmt.Sprintf("%s (%s) ", prompt, hint))
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return defaultValue, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.out, "Answer y or n.")
	}
}

func (p *linePrompter) AuthToken() (string, error) {
	for {
		token, err := p.Password("Paste your authentication token:")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token), nil
		}
		fmt.Fprintln(p.out, "A token is required.")
	}
}

func (p *linePrompter) ConfirmDeletion(requiredValue string) error {
	answer, err := p.ask(fmt.Sprintf("Type %s to confirm deletion: ", requiredValue))
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(answer), requiredValue) {
		return fmt.Errorf("You entered %s", answer)
	}
	return nil
}

func (p *linePrompter) InputHostname() (string, error) {
	for {
		answer, err := p.ask("Hostname: ")
		if err != nil {
			return "", err
		}
		answer = strings.TrimSpace(answer)
		if err := ghinstance.HostnameValidator(answer); err != nil {
			fmt.Fprintf(p.out, "%s\n", err)
			continue
		}
		return answer, nil
	}
}

// MarkdownEditor reads the body from stdin instead of opening an editor, since
// there is no process to open (see pkg/surveyext). A line holding a single dot
// ends it - the convention mail clients have used for this since before there
// were editors to launch.
func (p *linePrompter) MarkdownEditor(prompt, defaultValue string, blankAllowed bool) (string, error) {
	fmt.Fprintf(p.out, "%s\n", prompt)
	if defaultValue != "" {
		fmt.Fprintf(p.out, "Current text:\n%s\n", defaultValue)
	}
	fmt.Fprintln(p.out, "Type the text, then a line with only a dot (.) to finish.")
	if blankAllowed {
		fmt.Fprintln(p.out, "A dot on the first line leaves it empty.")
	}

	var lines []string
	for {
		line, err := p.readLine()
		if err != nil {
			if errors.Is(err, ErrEOF) {
				break
			}
			return "", err
		}
		if line == "." {
			break
		}
		lines = append(lines, line)
	}

	text := strings.Join(lines, "\n")
	if text == "" && !blankAllowed {
		return defaultValue, nil
	}
	return text, nil
}
