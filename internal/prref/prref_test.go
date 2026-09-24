package prref

import "testing"

func TestFormat(t *testing.T) {
	tests := []struct {
		name   string
		owner  string
		repo   string
		number int
		want   string
	}{
		{name: "lowercase", owner: "octocat", repo: "hello", number: 42, want: "github:octocat/hello#42"},
		{name: "mixed case normalized", owner: "OctoCat", repo: "Hello-World", number: 7, want: "github:octocat/hello-world#7"},
		{name: "surrounding whitespace trimmed", owner: "  octo ", repo: " hello\t", number: 1, want: "github:octo/hello#1"},
		{name: "dots underscores dashes", owner: "my_org", repo: "repo.js", number: 3, want: "github:my_org/repo.js#3"},
		{name: "empty owner", owner: "", repo: "hello", number: 1, want: ""},
		{name: "empty repo", owner: "octo", repo: "", number: 1, want: ""},
		{name: "owner with slash", owner: "octo/cat", repo: "hello", number: 1, want: ""},
		{name: "repo with hash", owner: "octo", repo: "hel#lo", number: 1, want: ""},
		{name: "repo with space", owner: "octo", repo: "hel lo", number: 1, want: ""},
		{name: "dot owner", owner: ".", repo: "hello", number: 1, want: ""},
		{name: "dotdot repo", owner: "octo", repo: "..", number: 1, want: ""},
		{name: "zero number", owner: "octo", repo: "hello", number: 0, want: ""},
		{name: "negative number", owner: "octo", repo: "hello", number: -4, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(tc.owner, tc.repo, tc.number); got != tc.want {
				t.Fatalf("Format(%q, %q, %d) = %q, want %q", tc.owner, tc.repo, tc.number, got, tc.want)
			}
		})
	}
}

func TestRefKey(t *testing.T) {
	if got := (Ref{Owner: "Octo", Repo: "Hello", Number: 9}).Key(); got != "github:octo/hello#9" {
		t.Fatalf("Key() = %q", got)
	}
	if got := (Ref{}).Key(); got != "" {
		t.Fatalf("zero Ref Key() = %q, want empty", got)
	}
}

func TestParse(t *testing.T) {
	valid := []struct {
		name string
		key  string
		want Ref
	}{
		{name: "canonical", key: "github:octocat/hello#42", want: Ref{Owner: "octocat", Repo: "hello", Number: 42}},
		{name: "legacy unprefixed", key: "octocat/hello#42", want: Ref{Owner: "octocat", Repo: "hello", Number: 42}},
		{name: "uppercase prefix", key: "GITHUB:octocat/hello#42", want: Ref{Owner: "octocat", Repo: "hello", Number: 42}},
		{name: "mixed case lowercased", key: "github:OctoCat/Hello.World#5", want: Ref{Owner: "octocat", Repo: "hello.world", Number: 5}},
		{name: "legacy mixed case lowercased", key: "Octo/Hello#5", want: Ref{Owner: "octo", Repo: "hello", Number: 5}},
		{name: "surrounding whitespace", key: "  github:octo/hello#1  ", want: Ref{Owner: "octo", Repo: "hello", Number: 1}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.key)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.key, err)
			}
			if got != tc.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tc.key, got, tc.want)
			}
		})
	}

	invalid := []struct {
		name string
		key  string
	}{
		{name: "empty", key: ""},
		{name: "prefix only", key: "github:"},
		{name: "missing hash", key: "github:octo/hello"},
		{name: "missing number", key: "github:octo/hello#"},
		{name: "missing slash", key: "github:octohello#1"},
		{name: "empty owner", key: "github:/hello#1"},
		{name: "empty repo", key: "github:octo/#1"},
		{name: "leading zero", key: "github:octo/hello#042"},
		{name: "zero", key: "github:octo/hello#0"},
		{name: "negative", key: "github:octo/hello#-3"},
		{name: "plus sign", key: "github:octo/hello#+3"},
		{name: "non numeric", key: "github:octo/hello#abc"},
		{name: "trailing junk", key: "github:octo/hello#3x"},
		{name: "double hash", key: "github:octo/hello#3#4"},
		{name: "extra slash", key: "github:octo/hello/extra#3"},
		{name: "leading slash", key: "github:/octo/hello#3"},
		{name: "dotdot owner", key: "github:../hello#3"},
		{name: "dotdot repo", key: "github:octo/..#3"},
		{name: "dot repo", key: "github:octo/.#3"},
		{name: "whitespace inside", key: "github:octo/hel lo#3"},
		{name: "other provider prefix", key: "gitlab:octo/hello#3"},
	}
	for _, tc := range invalid {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			if got, err := Parse(tc.key); err == nil {
				t.Fatalf("Parse(%q) = %+v, want error", tc.key, got)
			}
		})
	}
}

func TestFromURL(t *testing.T) {
	want := Ref{Owner: "octo", Repo: "hello", Number: 42}
	valid := []struct {
		name string
		url  string
		want Ref
	}{
		{name: "https", url: "https://github.com/octo/hello/pull/42", want: want},
		{name: "http", url: "http://github.com/octo/hello/pull/42", want: want},
		{name: "www host", url: "https://www.github.com/octo/hello/pull/42", want: want},
		{name: "uppercase host", url: "https://GitHub.com/octo/hello/pull/42", want: want},
		{name: "dot git repo", url: "https://github.com/octo/hello.git/pull/42", want: want},
		{name: "files subpath", url: "https://github.com/octo/hello/pull/42/files", want: want},
		{name: "trailing slash", url: "https://github.com/octo/hello/pull/42/", want: want},
		{name: "pulls path", url: "https://github.com/octo/hello/pulls/42", want: want},
		{name: "query and fragment", url: "https://github.com/octo/hello/pull/42?w=1#discussion", want: want},
		{name: "whitespace", url: "  https://github.com/octo/hello/pull/42\n", want: want},
		{name: "mixed case lowercased", url: "https://github.com/Octo/Hello/pull/42", want: want},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FromURL(tc.url)
			if !ok {
				t.Fatalf("FromURL(%q) not ok", tc.url)
			}
			if got != tc.want {
				t.Fatalf("FromURL(%q) = %+v, want %+v", tc.url, got, tc.want)
			}
			if key := got.Key(); key != "github:octo/hello#42" {
				t.Fatalf("FromURL(%q).Key() = %q", tc.url, key)
			}
		})
	}

	invalid := []struct {
		name string
		url  string
	}{
		{name: "empty", url: ""},
		{name: "non github host", url: "https://gitlab.com/octo/hello/pull/42"},
		{name: "github subdomain", url: "https://api.github.com/octo/hello/pull/42"},
		{name: "enterprise host", url: "https://github.example.com/octo/hello/pull/42"},
		{name: "ssh scheme", url: "ssh://github.com/octo/hello/pull/42"},
		{name: "no scheme", url: "github.com/octo/hello/pull/42"},
		{name: "issues url", url: "https://github.com/octo/hello/issues/42"},
		{name: "repo url", url: "https://github.com/octo/hello"},
		{name: "pull without number", url: "https://github.com/octo/hello/pull"},
		{name: "zero number", url: "https://github.com/octo/hello/pull/0"},
		{name: "negative number", url: "https://github.com/octo/hello/pull/-1"},
		{name: "non numeric", url: "https://github.com/octo/hello/pull/abc"},
		{name: "dotdot owner", url: "https://github.com/../hello/pull/42"},
		{name: "not a url", url: "JIRA-1"},
	}
	for _, tc := range invalid {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			if got, ok := FromURL(tc.url); ok {
				t.Fatalf("FromURL(%q) = %+v, want not ok", tc.url, got)
			}
		})
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	cases := []Ref{
		{Owner: "octo", Repo: "hello", Number: 1},
		{Owner: "Octo-Org", Repo: "Hello.World", Number: 123456},
		{Owner: "a_b", Repo: "c-d", Number: 99},
	}
	for _, in := range cases {
		key := Format(in.Owner, in.Repo, in.Number)
		if key == "" {
			t.Fatalf("Format(%+v) empty", in)
		}
		got, err := Parse(key)
		if err != nil {
			t.Fatalf("Parse(Format(%+v)=%q) error: %v", in, key, err)
		}
		if got.Key() != key {
			t.Fatalf("round trip key = %q, want %q", got.Key(), key)
		}
		if got.Number != in.Number {
			t.Fatalf("round trip number = %d, want %d", got.Number, in.Number)
		}
	}
}
