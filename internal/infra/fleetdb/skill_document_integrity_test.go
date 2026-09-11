package fleetdb

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestSkillDocumentReadRejectsIncompleteSource(t *testing.T) {
	for _, body := range []string{
		``, `null`, `{}`,
		`{"path":"SKILL.md","content":"","revision":"rev","skill_ref":"workspace:a","executable":null}`,
		`{"path":"SKILL.md","revision":"rev","skill_ref":"workspace:a"}`,
		`{"path":"SKILL.md","content":null,"revision":"rev","skill_ref":"workspace:a"}`,
		`{"path":"SKILL.md","content":"","skill_ref":"workspace:a"}`,
		`{"path":"SKILL.md","content":"","revision":"","skill_ref":"workspace:a"}`,
		`{"path":"wrong","content":"","revision":"rev","skill_ref":"workspace:a"}`,
		`{"path":"SKILL.md","content":"","revision":"rev","skill_ref":"workspace:b"}`,
	} {
		t.Run(body, func(t *testing.T) {
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
			defer closeFn()
			doc, err := client.Skills().GetFile(t.Context(), "WS", domain.SkillRef{Scope: domain.SkillScopeWorkspace, Name: "a"}, "SKILL.md")
			require.Error(t, err)
			require.Nil(t, doc)
		})
	}
}

func TestSkillDocumentReadExplicitEmptyAndVersion(t *testing.T) {
	for _, etag := range []string{`"rev"`, `"different"`} {
		t.Run(etag, func(t *testing.T) {
			client, closeFn := newSkillTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("ETag", etag)
				_, _ = w.Write([]byte(`{"path":"SKILL.md","content":"","revision":"rev","skill_ref":"workspace:a"}`))
			})
			defer closeFn()
			doc, err := client.Skills().GetFile(t.Context(), "WS", domain.SkillRef{Scope: domain.SkillScopeWorkspace, Name: "a"}, "SKILL.md")
			if etag == `"different"` {
				require.Error(t, err)
				require.Nil(t, doc)
				return
			}
			require.NoError(t, err)
			require.Empty(t, doc.Content)
			require.Equal(t, "rev", doc.Revision)
		})
	}
}
