package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestPhase5InteractionChatOwnershipRatchet keeps provider-specific
// conversation reads and controlled-runtime delivery behind Interaction's
// chat runtime adapter. Production callers may receive ChatAPI or
// ChatMessenger; they may not call Interaction's provider/runtime helpers
// directly.
func TestPhase5InteractionChatOwnershipRatchet(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var blockers []string
	err = filepath.WalkDir(
		filepath.Join(root, "internal"),
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				if relative == "internal/infra/interactionlead" ||
					relative == "internal/infra/interactionchat" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}
			return collectDirectInteractionChatCalls(
				root,
				path,
				&blockers,
			)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(blockers)
	if len(blockers) != 0 {
		t.Fatalf(
			"Phase 5 Interaction chat has direct provider/runtime callers outside its infrastructure adapter: %v",
			blockers,
		)
	}
}

func collectDirectInteractionChatCalls(
	root,
	path string,
	blockers *[]string,
) error {
	files := token.NewFileSet()
	source, err := parser.ParseFile(
		files,
		path,
		nil,
		parser.ImportsOnly,
	)
	if err != nil {
		return err
	}
	aliases := make(map[string]struct{})
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	for _, imported := range source.Imports {
		if strings.Trim(imported.Path.Value, `"`) !=
			"github.com/tysonthomas9/loomcli/internal/infra/interactionlead" {
			continue
		}
		name := "leadcontrol"
		if imported.Name != nil {
			name = imported.Name.Name
		}
		switch name {
		case "_":
			continue
		case ".":
			position := files.Position(imported.Pos())
			*blockers = append(
				*blockers,
				filepath.ToSlash(relative)+":"+
					strconv.Itoa(position.Line)+":dot-import",
			)
		default:
			aliases[name] = struct{}{}
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	source, err = parser.ParseFile(files, path, nil, 0)
	if err != nil {
		return err
	}
	disallowed := map[string]struct{}{
		"DeliverCurrentAssignment":             {},
		"DeliverCurrentAssignmentToCodex":      {},
		"DeliverLeadMessage":                   {},
		"DeliverLeadMessageToCodex":            {},
		"DeliverLeadMessageToCodexWithOptions": {},
		"DeliverLeadMessageWithOptions":        {},
		"DeliverPendingLeadMessages":           {},
		"DeliverPendingLeadMessagesToCodex":    {},
		"DialCodexAppServer":                   {},
		"HarnessRuntimeMetadataFromSession":    {},
		"IsControlledLeadBackend":              {},
		"MarkAssignmentDelivered":              {},
		"MarkAssignmentDeliveryAttempt":        {},
		"MarkLeadMessageDeliveryAttempt":       {},
		"RuntimeMetadataFromSession":           {},
	}
	ast.Inspect(source, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if _, ok := aliases[identifier.Name]; !ok {
			return true
		}
		if _, ok := disallowed[selector.Sel.Name]; !ok {
			return true
		}
		position := files.Position(selector.Pos())
		*blockers = append(
			*blockers,
			filepath.ToSlash(relative)+":"+
				strconv.Itoa(position.Line)+":"+
				selector.Sel.Name,
		)
		return true
	})
	return nil
}

func TestCollectDirectInteractionChatCallsRejectsAliasesReferencesAndDotImports(
	t *testing.T,
) {
	root := t.TempDir()
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "aliased function reference",
			body: `package sample
import lc "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
var deliver = lc.DeliverLeadMessageToCodexWithOptions
`,
			want: []string{
				"sample.go:3:DeliverLeadMessageToCodexWithOptions",
			},
		},
		{
			name: "dot import",
			body: `package sample
import . "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
func use() { _, _ = DeliverLeadMessage, DeliverCurrentAssignment }
`,
			want: []string{"sample.go:2:dot-import"},
		},
		{
			name: "blank import",
			body: `package sample
import _ "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, "sample.go")
			if err := os.WriteFile(
				path,
				[]byte(test.body),
				0o644,
			); err != nil {
				t.Fatal(err)
			}
			var blockers []string
			if err := collectDirectInteractionChatCalls(
				root,
				path,
				&blockers,
			); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(blockers, test.want) {
				t.Fatalf("blockers = %v, want %v", blockers, test.want)
			}
		})
	}
}
