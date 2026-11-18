package cli

import "testing"

// Log lines come from an agent's output and from pages it fetched, so a line can
// carry terminal escape sequences. pixelgo returns the record as logged - its
// copy should be the record - and says in docs/API.md that filtering is the
// client's job. This is that job, and it is the last thing between an agent's
// output and a terminal.
func TestSanitizeStripsControlCharacters(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "an ordinary line is untouched",
			in:   "[INFO ] ai_loop: agent=reviewer iteration=3",
			want: "[INFO ] ai_loop: agent=reviewer iteration=3",
		},
		{
			name: "tabs survive, because log lines align with them",
			in:   "[WARN ]\tagent\tstopped",
			want: "[WARN ]\tagent\tstopped",
		},
		{
			// The one that matters: this sequence clears the screen and moves
			// the cursor home, so a line containing it can erase the lines
			// printed before it - including the ones it would rather you
			// did not read.
			name: "an escape sequence is removed",
			in:   "[INFO ] fine\x1b[2J\x1b[Hnothing to see",
			want: "[INFO ] fine[2J[Hnothing to see",
		},
		{
			name: "a carriage return cannot overwrite the line",
			in:   "[ERROR] something broke\rall good",
			want: "[ERROR] something brokeall good",
		},
		{
			name: "a bell does not ring",
			in:   "done\x07",
			want: "done",
		},
		{
			name: "delete is removed too",
			in:   "a\x7fb",
			want: "ab",
		},
		{
			name: "text outside ASCII is kept",
			in:   "agent=recenzor pornit",
			want: "agent=recenzor pornit",
		},
	}

	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("%s:\n  in:   %q\n  got:  %q\n  want: %q", c.name, c.in, got, c.want)
		}
	}
}

// One corrupt byte must not be passed through to a terminal: replaced, so the
// rest of the output is unaffected.
func TestSanitizeReplacesInvalidUTF8(t *testing.T) {
	got := sanitize("before\xffafter")
	if got != "before\uFFFDafter" {
		t.Errorf("got %q", got)
	}
}
