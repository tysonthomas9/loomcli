package main

import (
	"bufio"
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
)

const directivePrefix = "//go:"

type lineRange struct {
	start int
	end   int
}

type violation struct {
	path string
	line int
	text string
}

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.DirFS(".")))
}

func run(in io.Reader, out io.Writer, fsys fs.FS) int {
	ranges, order, err := parseRanges(in)
	if err != nil {
		_, _ = fmt.Fprintf(out, "commentscan: %v\n", err)
		return 2
	}
	var all []violation
	for _, path := range order {
		src, err := fs.ReadFile(fsys, path)
		if err != nil {
			_, _ = fmt.Fprintf(out, "commentscan: read %s: %v\n", path, err)
			return 2
		}
		all = append(all, scanFile(path, src, ranges[path])...)
	}
	for _, v := range all {
		_, _ = fmt.Fprintf(out, "%s:%d: %s\n", v.path, v.line, v.text)
	}
	if len(all) > 0 {
		return 1
	}
	return 0
}

func parseRanges(in io.Reader) (map[string][]lineRange, []string, error) {
	ranges := map[string][]lineRange{}
	var order []string
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, nil, fmt.Errorf("malformed range line %q", line)
		}
		start, errStart := strconv.Atoi(fields[1])
		end, errEnd := strconv.Atoi(fields[2])
		if errStart != nil || errEnd != nil || start < 1 || end < start {
			return nil, nil, fmt.Errorf("malformed range line %q", line)
		}
		path := fields[0]
		if _, seen := ranges[path]; !seen {
			order = append(order, path)
		}
		ranges[path] = append(ranges[path], lineRange{start: start, end: end})
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	return ranges, order, nil
}

func scanFile(path string, src []byte, added []lineRange) []violation {
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	lines := strings.Split(string(src), "\n")
	flagged := map[int]bool{}
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT || strings.HasPrefix(lit, directivePrefix) {
			continue
		}
		first := file.Line(pos)
		last := first + strings.Count(lit, "\n")
		for ln := first; ln <= last; ln++ {
			if isAdded(ln, added) {
				flagged[ln] = true
			}
		}
	}
	result := make([]violation, 0, len(flagged))
	for ln := range flagged {
		text := ""
		if ln-1 < len(lines) {
			text = strings.TrimSpace(lines[ln-1])
		}
		result = append(result, violation{path: path, line: ln, text: text})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].line < result[j].line })
	return result
}

func isAdded(line int, added []lineRange) bool {
	for _, r := range added {
		if line >= r.start && line <= r.end {
			return true
		}
	}
	return false
}
