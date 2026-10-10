package harnessemu

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// A scripted tool's permission and question asks, as OpenCode b30c4d0
// raises them: the question tool's form, the wait for a reply, and the
// routes that list, read and answer them.

// Question is one question of OpenCode's question tool.
type Question struct {
	Header   string `json:"header"`
	Question string `json:"question"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
	} `json:"options"`
	Multiple bool `json:"multiple,omitempty"`
}

// questionForm is the form OpenCode's question tool asks: one field per
// question, keyed q0, q1, ..., whose options' values are their labels.
func questionForm(qs []Question) map[string]any {
	fields := make([]map[string]any, len(qs))
	for i, q := range qs {
		typ := "string"
		if q.Multiple {
			typ = "multiselect"
		}
		opts := make([]map[string]any, len(q.Options))
		for j, o := range q.Options {
			opts[j] = map[string]any{"value": o.Label, "label": o.Label, "description": o.Description}
		}
		fields[i] = map[string]any{"key": "q" + strconv.Itoa(i), "title": q.Header, "description": q.Question, "type": typ, "options": opts, "custom": true}
	}
	return map[string]any{"title": "Questions", "metadata": map[string]any{"kind": "question"}, "fields": fields}
}

// formAnswers is the question tool's output for a form answer: its answers,
// one list per question.
func formAnswers(qs []Question, got map[string]any) string {
	answers := make([][]string, len(qs))
	for i := range qs {
		switch v := got["q"+strconv.Itoa(i)].(type) {
		case string:
			answers[i] = []string{v}
		case []any:
			for _, a := range v {
				answers[i] = append(answers[i], fmt.Sprint(a))
			}
		}
	}
	b, _ := json.Marshal(map[string]any{"answers": answers})
	return string(b)
}

// await opens ask id (per_ a permission, frm_ a form) with info, as OpenCode
// does, and waits for its reply without the lock: a permission reply's body
// or a form's answer. false means r stopped or was replaced, or the server
// is closing; the ask is then gone.
func (s *Server) await(sid string, r *run, id string, info map[string]any) (any, bool) {
	ss := s.st.Sessions[sid]
	info = with(with(info, "id", id), "sessionID", sid)
	if ss.Asks == nil {
		ss.Asks = map[string]any{}
	}
	ss.Asks[id] = info
	ch := make(chan any, 1)
	s.replies[id] = ch
	if strings.HasPrefix(id, "per_") {
		s.emit(sid, "permission.asked", clone(info))
	} else {
		s.emit(sid, "form.created", map[string]any{"form": info})
	}
	s.mu.Unlock()
	var ans any
	select {
	case ans = <-ch:
	case <-r.stop:
	case <-s.quit:
	}
	s.mu.Lock()
	delete(s.replies, id)
	if ss := s.st.Sessions[sid]; ss != nil {
		delete(ss.Asks, id)
	}
	select {
	case <-s.quit:
		return nil, false
	default:
	}
	ss = s.st.Sessions[sid]
	return ans, ans != nil && ss != nil && ss.Running == r
}

// answer replies to session ss's pending ask id with ans and emits the
// reply's event; false when no such ask is pending.
func (s *Server) answer(sid string, ss *session, id, event string, data map[string]any, ans any) bool {
	ch, ok := s.replies[id]
	if _, pending := ss.Asks[id]; !ok || !pending {
		return false
	}
	delete(ss.Asks, id)
	s.emit(sid, event, data)
	ch <- ans
	return true
}

// askRoutes serve the pending permission and form asks a scripted tool
// raised: list, read and reply, as OpenCode b30c4d0 does.
func (s *Server) askRoutes(mux *http.ServeMux) {
	list := func(prefix string) http.HandlerFunc {
		return s.h(func(w http.ResponseWriter, _ *http.Request, ss *session, _ map[string]any) {
			out := []any{}
			for _, id := range slices.Sorted(maps.Keys(ss.Asks)) {
				if strings.HasPrefix(id, prefix) {
					out = append(out, ss.Asks[id])
				}
			}
			reply(w, 200, map[string]any{"data": out})
		})
	}
	get := func(tag string) http.HandlerFunc {
		return s.h(func(w http.ResponseWriter, r *http.Request, ss *session, _ map[string]any) {
			if a, ok := ss.Asks[r.PathValue("ask")]; ok {
				reply(w, 200, map[string]any{"data": a})
				return
			}
			reply(w, 404, map[string]string{"_tag": tag, "message": "no request"})
		})
	}
	mux.HandleFunc("GET /api/session/{id}/permission", list("per_"))
	mux.HandleFunc("GET /api/session/{id}/form", list("frm_"))
	mux.HandleFunc("GET /api/session/{id}/permission/{ask}", get("PermissionNotFoundError"))
	mux.HandleFunc("GET /api/session/{id}/form/{ask}", get("FormNotFoundError"))
	mux.HandleFunc("POST /api/session/{id}/permission/{ask}/reply", s.h(func(w http.ResponseWriter, r *http.Request, ss *session, body map[string]any) {
		id := r.PathValue("ask")
		if !s.answer(r.PathValue("id"), ss, id, "permission.replied", map[string]any{"requestID": id, "reply": body["decision"]}, body) {
			reply(w, 404, map[string]string{"_tag": "PermissionNotFoundError", "message": "no request"})
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/session/{id}/form/{ask}/reply", s.h(func(w http.ResponseWriter, r *http.Request, ss *session, body map[string]any) {
		id := r.PathValue("ask")
		ans, _ := body["answer"].(map[string]any)
		if ans == nil {
			ans = map[string]any{}
		}
		if !s.answer(r.PathValue("id"), ss, id, "form.replied", map[string]any{"id": id, "answer": ans}, ans) {
			reply(w, 404, map[string]string{"_tag": "FormNotFoundError", "message": "no form"})
			return
		}
		w.WriteHeader(204)
	}))
}
