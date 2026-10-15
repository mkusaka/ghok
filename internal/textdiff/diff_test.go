package textdiff

import (
	"bytes"
	"testing"
)

func TestDiff(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{
			name: "replacement",
			old:  "before\nold\nafter\n",
			new:  "before\nnew\nafter\n",
			want: "@@ -1,3 +1,3 @@\n before\n-old\n+new\n after\n",
		},
		{
			name: "addition",
			new:  "added\n",
			want: "@@ -0,0 +1,1 @@\n+added\n",
		},
		{
			name: "removal",
			old:  "removed\n",
			want: "@@ -1,1 +0,0 @@\n-removed\n",
		},
		{
			name: "missing final newline",
			old:  "same",
			new:  "same\n",
			want: "@@ -1,1 +1,1 @@\n-same\n\\ No newline at end of file\n+same\n",
		},
		{
			name: "new missing final newline",
			old:  "same\n",
			new:  "same",
			want: "@@ -1,1 +1,1 @@\n-same\n+same\n\\ No newline at end of file\n",
		},
		{
			name: "CRLF versus LF",
			old:  "same\r\n",
			new:  "same\n",
			want: "@@ -1,1 +1,1 @@\n-same^M\n+same\n",
		},
		{
			name: "bare CR does not split lines",
			old:  "one\rtwo\n",
			new:  "one\rthree\n",
			want: "@@ -1,1 +1,1 @@\n-one^Mtwo\n+one^Mthree\n",
		},
		{
			name: "invalid UTF8 preserved",
			old:  "\xff\xfe old\n",
			new:  "\xff\xfe new\n",
			want: "@@ -1,1 +1,1 @@\n-\xff\xfe old\n+\xff\xfe new\n",
		},
		{
			name: "repeated lines stay around unique anchor",
			old:  "start\nrepeat\nleft\nrepeat\nanchor-a\nanchor-b\nanchor-c\nrepeat\nright\nrepeat\nend\n",
			new:  "start\nrepeat\nright\nrepeat\nanchor-a\nanchor-b\nanchor-c\nrepeat\nleft\nrepeat\nend\n",
			want: "@@ -1,11 +1,11 @@\n start\n repeat\n-left\n+right\n repeat\n anchor-a\n anchor-b\n anchor-c\n repeat\n-right\n+left\n repeat\n end\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := []byte("diff old new\n--- old\n+++ new\n" + tt.want)
			if got := Diff("old", []byte(tt.old), "new", []byte(tt.new)); !bytes.Equal(got, want) {
				t.Fatalf("Diff() = %q, want %q", got, want)
			}
		})
	}
}

func TestDiffIdentical(t *testing.T) {
	for _, text := range []string{"", "same\n", "same", "\xff\r\n"} {
		if got := Diff("old", []byte(text), "new", []byte(text)); got != nil {
			t.Errorf("Diff() for identical %q = %q, want nil", text, got)
		}
	}
}
