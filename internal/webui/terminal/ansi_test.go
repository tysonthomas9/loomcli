package terminal

import "testing"

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no escapes",
			in:   "hello world",
			want: "hello world",
		},
		{
			name: "color codes",
			in:   "\x1b[31mred\x1b[0m",
			want: "red",
		},
		{
			name: "bold and underline",
			in:   "\x1b[1mbold\x1b[0m \x1b[4munderlined\x1b[0m",
			want: "bold underlined",
		},
		{
			name: "cursor movement",
			in:   "\x1b[2Jhello\x1b[H",
			want: "hello",
		},
		{
			name: "OSC title with BEL",
			in:   "\x1b]0;my title\x07text",
			want: "text",
		},
		{
			name: "OSC title with ST",
			in:   "\x1b]0;my title\x1b\\text",
			want: "text",
		},
		{
			name: "charset switching",
			in:   "\x1b(Bhello",
			want: "hello",
		},
		{
			name: "multibyte UTF-8 preserved",
			in:   "\x1b[32m日本語\x1b[0m",
			want: "日本語",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "multiple inline sequences",
			in:   "\x1b[31mR\x1b[32mG\x1b[34mB\x1b[0m",
			want: "RGB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripANSI(tt.in)
			if got != tt.want {
				t.Errorf("StripANSI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
