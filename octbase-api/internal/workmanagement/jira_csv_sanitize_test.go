package workmanagement

import "testing"

// Guards the CSV formula-injection fix (2026-07-14 assessment, L5): cells that a
// spreadsheet would evaluate as a formula must be neutralized with a leading
// quote, while ordinary text is left untouched.
func TestSanitizeCSVCell(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"Normal title", "Normal title"},
		{"=1+1", "'=1+1"},
		{"+CMD()", "'+CMD()"},
		{"-2+3", "'-2+3"},
		{"@SUM(A1)", "'@SUM(A1)"},
		{"=HYPERLINK(\"http://evil\")", "'=HYPERLINK(\"http://evil\")"},
		{"\tleading tab", "'\tleading tab"},
		{"\rleading cr", "'\rleading cr"},
		{"a=b (not leading)", "a=b (not leading)"},
	}
	for _, c := range cases {
		if got := sanitizeCSVCell(c.in); got != c.want {
			t.Errorf("sanitizeCSVCell(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
