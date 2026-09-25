package firewall

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The notice's templates are translated in the page (D-12), which the UI's i18n check cannot see:
// the keys come from here. Every NewNote template must have its catalogue entry.
func TestNoteTemplatesTranslated(t *testing.T) {
	cat, err := os.ReadFile("../../ui/src/lib/i18n.svelte.ts")
	if err != nil {
		t.Skip("no UI catalogue:", err)
	}
	re := regexp.MustCompile(`NewNote\(("(?:[^"\\]|\\.)*")`)
	n := 0
	for _, f := range []string{"migrate.go", "../system/fwrules.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllSubmatch(src, -1) {
			text, err := strconv.Unquote(string(m[1]))
			if err != nil {
				t.Fatal(err)
			}
			n++
			single := "'" + regexp.MustCompile(`'`).ReplaceAllString(text, `\'`) + "':"
			double := strconv.Quote(text) + ":"
			if !regexp.MustCompile(regexp.QuoteMeta(single) + "|" + regexp.QuoteMeta(double)).Match(cat) {
				t.Errorf("no German entry for %q", text)
			}
		}
	}
	if n < 10 {
		t.Fatalf("only %d templates found", n)
	}
}
