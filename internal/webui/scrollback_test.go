package webui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestScrollbackBuffer_BasicAppend(t *testing.T) {
	buf := NewScrollbackBuffer(100)
	buf.Append([]byte("hello\nworld\n"))

	lines := buf.Lines()
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "hello" || lines[1] != "world" {
		t.Fatalf("unexpected lines: %v", lines)
	}
	if buf.LineCount() != 2 {
		t.Fatalf("expected LineCount 2, got %d", buf.LineCount())
	}
	if buf.TruncatedCount() != 0 {
		t.Fatalf("expected TruncatedCount 0, got %d", buf.TruncatedCount())
	}
}

func TestScrollbackBuffer_PartialLines(t *testing.T) {
	buf := NewScrollbackBuffer(100)
	// First chunk has no trailing newline — partial line.
	buf.Append([]byte("hel"))
	if buf.LineCount() != 0 {
		t.Fatalf("expected 0 complete lines, got %d", buf.LineCount())
	}

	// Second chunk completes the line and starts another partial.
	buf.Append([]byte("lo\nwor"))
	if buf.LineCount() != 1 {
		t.Fatalf("expected 1 complete line, got %d", buf.LineCount())
	}
	lines := buf.Lines()
	if lines[0] != "hello" {
		t.Fatalf("expected 'hello', got %q", lines[0])
	}

	// Third chunk completes the second line.
	buf.Append([]byte("ld\n"))
	if buf.LineCount() != 2 {
		t.Fatalf("expected 2 lines, got %d", buf.LineCount())
	}
	lines = buf.Lines()
	if lines[1] != "world" {
		t.Fatalf("expected 'world', got %q", lines[1])
	}
}

func TestScrollbackBuffer_Truncation(t *testing.T) {
	buf := NewScrollbackBuffer(5)

	// Append 8 lines, buffer holds max 5.
	for i := 0; i < 8; i++ {
		buf.Append([]byte(fmt.Sprintf("line%d\n", i)))
	}

	if buf.LineCount() != 5 {
		t.Fatalf("expected 5 lines, got %d", buf.LineCount())
	}
	if buf.TruncatedCount() != 3 {
		t.Fatalf("expected 3 truncated, got %d", buf.TruncatedCount())
	}

	// Remaining lines should be the last 5: line3..line7
	lines := buf.Lines()
	for i, line := range lines {
		expected := fmt.Sprintf("line%d", i+3)
		if line != expected {
			t.Errorf("lines[%d] = %q, want %q", i, line, expected)
		}
	}
}

func TestScrollbackBuffer_Clear(t *testing.T) {
	buf := NewScrollbackBuffer(10)
	buf.Append([]byte("a\nb\nc\n"))
	buf.Clear()

	if buf.LineCount() != 0 {
		t.Fatalf("expected 0 after clear, got %d", buf.LineCount())
	}
	if buf.TruncatedCount() != 0 {
		t.Fatalf("expected 0 truncated after clear, got %d", buf.TruncatedCount())
	}
	lines := buf.Lines()
	if len(lines) != 0 {
		t.Fatalf("expected empty lines, got %v", lines)
	}
}

func TestScrollbackBuffer_EmptyAppend(t *testing.T) {
	buf := NewScrollbackBuffer(10)
	buf.Append([]byte{})
	buf.Append(nil)
	if buf.LineCount() != 0 {
		t.Fatalf("expected 0, got %d", buf.LineCount())
	}
}

func TestScrollbackBuffer_ConcurrentAccess(t *testing.T) {
	buf := NewScrollbackBuffer(1000)
	var wg sync.WaitGroup

	// Multiple concurrent writers.
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				buf.Append([]byte(fmt.Sprintf("g%d-line%d\n", id, i)))
			}
		}(g)
	}

	// Concurrent reader.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = buf.Lines()
			_ = buf.LineCount()
			_ = buf.TruncatedCount()
		}
	}()

	wg.Wait()

	// All 1000 lines should fit in the buffer.
	if buf.LineCount() != 1000 {
		t.Fatalf("expected 1000 lines, got %d", buf.LineCount())
	}
}

func TestScrollbackBuffer_ExactCapacity(t *testing.T) {
	buf := NewScrollbackBuffer(3)
	buf.Append([]byte("a\nb\nc\n"))
	if buf.LineCount() != 3 {
		t.Fatalf("expected 3, got %d", buf.LineCount())
	}
	if buf.TruncatedCount() != 0 {
		t.Fatalf("expected 0 truncated, got %d", buf.TruncatedCount())
	}
}

func TestScrollbackBuffer_MultilineChunk(t *testing.T) {
	buf := NewScrollbackBuffer(100)
	// A single large chunk with multiple lines.
	data := strings.Repeat("x\n", 50)
	buf.Append([]byte(data))

	if buf.LineCount() != 50 {
		t.Fatalf("expected 50 lines, got %d", buf.LineCount())
	}
}

func TestScrollbackBuffer_DefaultMaxLines(t *testing.T) {
	buf := NewScrollbackBuffer(0)
	// Should use defaultScrollbackMaxLines.
	if len(buf.lines) != defaultScrollbackMaxLines {
		t.Fatalf("expected %d, got %d", defaultScrollbackMaxLines, len(buf.lines))
	}
}
