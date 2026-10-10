package harnessemu

import "net/http"

// instructionRoutes play OpenCode 2.0.19's per-session instruction entries,
// which the adapter's Open uses to install a preset persona: PUT stores
// {"value": "..."} on the session under its name, DELETE removes it, each
// answering 204; a missing session is SessionNotFoundError (h). The
// emulator keeps the entries but does not send them to the fake model.
func (s *Server) instructionRoutes(mux *http.ServeMux) {
	const path = "/api/experimental/session/{id}/instructions/entries/{name}"
	mux.HandleFunc("PUT "+path, s.h(func(w http.ResponseWriter, r *http.Request, ss *session, body map[string]any) {
		value, ok := body["value"].(string)
		if !ok {
			reply(w, 400, map[string]string{"_tag": "HttpApiDecodeError", "message": "instruction entry value must be a string"})
			return
		}
		if ss.Instructions == nil {
			ss.Instructions = map[string]string{}
		}
		ss.Instructions[r.PathValue("name")] = value
		s.save()
		w.WriteHeader(204)
	}))
	mux.HandleFunc("DELETE "+path, s.h(func(w http.ResponseWriter, r *http.Request, ss *session, _ map[string]any) {
		delete(ss.Instructions, r.PathValue("name"))
		s.save()
		w.WriteHeader(204)
	}))
}
