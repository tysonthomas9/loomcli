package fleet

import "testing"

func TestRecoveryJSONSurrogateIdentity(t *testing.T) {
	valid := []string{
		`{"key":"\uD83D\uDE00"}`,
		`{"\ud83d\ude00":"paired key"}`,
		`{"key":"😀"}`,
		`{"key":"\\uD800"}`,
		`{"\\uD800":"literal escape key"}`,
		`{"key":"quote: \" then \uD83D\uDE00"}`,
		`{"key":"\u0000\ufffd"}`,
	}
	for _, document := range valid {
		t.Run("valid:"+document, func(t *testing.T) {
			if err := validateRecoveryJSON([]byte(document)); err != nil {
				t.Fatalf("valid document rejected: %v", err)
			}
		})
	}
	invalid := []string{
		`{"key":"\uD800"}`,
		`{"key":"\uDC00"}`,
		`{"\uD800":"invalid key"}`,
		`{"\uD800":1,"\uD801":2}`,
		`{"key":"\uD800x\uDC00"}`,
		`{"key":"\uD800\u0041"}`,
		`{"key":"\uD800\\uDC00"}`,
		`{"key":"\uD800\uD800"}`,
		`{"key":"\uD83D\uDE00\uDC00"}`,
		`{"\ud83d\ude00":1,"😀":2}`,
		`{"key":"\uD800`,
		"{\"key\":\"\xff\"}",
	}
	for _, document := range invalid {
		t.Run("invalid:"+document, func(t *testing.T) {
			if err := validateRecoveryJSON([]byte(document)); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}
