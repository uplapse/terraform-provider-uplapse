// Package mockapi is an in-memory stand-in for the parts of the Uplapse API the provider calls. It mirrors the
// behaviour that matters to Terraform: full-replacement PUT that keeps omitted nested objects, defaults the API
// fills in (status, interval, HTTPS port, heartbeat grace), masked credential headers, nested row ids that must match,
// plan-limit errors and 404s. Tests use it through httptest; cmd/mockserver serves it for manual runs.
package mockapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const mask = "********"

var secretHeader = regexp.MustCompile(`(?i)authorization|cookie|api[-_]?key|token|secret|password|session`)

// Server is the mock API. Create it with New and mount it with httptest.NewServer(s).
type Server struct {
	APIKey string
	// MaxMonitors makes create fail with the API's plan-limit error once reached (0 = unlimited).
	MaxMonitors int
	// MinInterval makes a shorter interval fail with the plan-limit error (0 = none). Default interval is 60.
	MinInterval int

	mu       sync.Mutex
	nextID   int64
	nextRow  int64
	monitors map[int64]map[string]any
	requests atomic.Int64
	// Calls records "METHOD path" of every request, for assertions.
	calls []string
}

// New returns an empty mock accepting apiKey.
func New(apiKey string) *Server {
	return &Server{APIKey: apiKey, nextID: 100, nextRow: 5000, monitors: map[int64]map[string]any{}}
}

// Calls returns the requests served so far.
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// Monitor returns a copy of a stored monitor, unmasked, or nil.
func (s *Server) Monitor(id int64) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.monitors[id]
	if !ok {
		return nil
	}
	return deepCopy(m)
}

// MonitorIDs returns the stored ids in order.
func (s *Server) MonitorIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int64
	for id := range s.monitors {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Mutate changes a stored monitor out of band, as an edit in the web app would (drift).
func (s *Server) Mutate(id int64, f func(m map[string]any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.monitors[id]; ok {
		f(m)
	}
}

// Remove deletes a monitor out of band.
func (s *Server) Remove(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.monitors, id)
}

// Put stores a monitor directly (for import tests) and returns its id.
func (s *Server) Put(m map[string]any) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	m = deepCopy(m)
	m["id"] = float64(id)
	s.monitors[id] = m
	return id
}

var monitorIDPath = regexp.MustCompile(`^/v1/monitor/(\d+)(/heartbeat)?$`)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	s.mu.Lock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	s.mu.Unlock()

	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "Full authentication is required to access this resource")
		return
	}

	switch {
	case r.URL.Path == "/v1/location-target/tree" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, locationTree)
	case r.URL.Path == "/v1/monitor/save" && r.Method == http.MethodPost:
		s.create(w, r)
	default:
		mm := monitorIDPath.FindStringSubmatch(r.URL.Path)
		if mm == nil {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "Not found")
			return
		}
		id, _ := strconv.ParseInt(mm[1], 10, 64)
		if mm[2] == "/heartbeat" {
			s.heartbeat(w, r, id)
			return
		}
		switch r.Method {
		case http.MethodGet:
			s.get(w, id)
		case http.MethodPut:
			s.update(w, r, id)
		case http.MethodDelete:
			s.delete(w, id)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
		}
	}
}

func (s *Server) authorized(r *http.Request) bool {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			key = strings.TrimPrefix(a, "Bearer ")
		}
	}
	return key != "" && key == s.APIKey
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "request body is required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MaxMonitors > 0 && len(s.monitors) >= s.MaxMonitors {
		writePlanLimit(w, fmt.Sprintf("Plan Free allows %d monitors. Upgrade to add more.", s.MaxMonitors))
		return
	}
	id := s.nextID + 1
	in["id"] = float64(id)
	if code, msg := s.normalize(in, nil); code != 0 {
		writeAny(w, code, msg)
		return
	}
	s.nextID = id
	s.monitors[id] = in
	writeJSON(w, http.StatusOK, masked(in))
}

func (s *Server) get(w http.ResponseWriter, id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.monitors[id]
	if !ok {
		// The real controller answers a missing monitor with an empty 404.
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, masked(m))
}

func (s *Server) update(w http.ResponseWriter, r *http.Request, id int64) {
	var in map[string]any
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "request body is required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.monitors[id]
	if !ok {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "")
		return
	}
	// MonitorOwnership: a nested row id must be the stored one.
	for _, k := range []string{"request", "notification"} {
		if nv, ok := in[k].(map[string]any); ok && nv["id"] != nil {
			old, _ := existing[k].(map[string]any)
			if old == nil || old["id"] != nv["id"] {
				writeErr(w, http.StatusNotFound, "NOT_FOUND", "")
				return
			}
		}
	}
	if code, msg := s.normalize(in, existing); code != 0 {
		writeAny(w, code, msg)
		return
	}
	in["id"] = float64(id)
	s.monitors[id] = in
	writeJSON(w, http.StatusOK, masked(in))
}

func (s *Server) delete(w http.ResponseWriter, id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.monitors[id]; !ok {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "")
		return
	}
	delete(s.monitors, id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.monitors[id]
	if !ok || m["type"] != "HEARTBEAT" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	key, _ := m["heartbeatKey"].(string)
	writeJSON(w, http.StatusOK, map[string]any{
		"monitorId": id, "heartbeatKey": key, "pingPath": "/v1/heartbeat/" + key,
		"graceSeconds": m["heartbeatGraceSeconds"],
	})
}

// normalize applies MonitorService.save's validation and defaults. existing is nil on create.
func (s *Server) normalize(in, existing map[string]any) (int, any) {
	name, _ := in["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return 400, errBody(400, "BAD_REQUEST", "name is required")
	}
	in["name"] = name
	typ, _ := in["type"].(string)
	if typ == "" {
		return 400, errBody(400, "BAD_REQUEST", "type is required")
	}
	url, _ := in["url"].(string)
	if typ != "HEARTBEAT" && strings.TrimSpace(url) == "" {
		return 400, errBody(400, "BAD_REQUEST", "url is required for "+typ+" monitors")
	}
	if (typ == "PORT" || typ == "TCP") && in["port"] == nil && (existing == nil || existing["port"] == nil) {
		return 400, errBody(400, "BAD_REQUEST", "port is required for "+typ+" monitors")
	}
	if typ == "KEYWORD" {
		if kw, _ := in["keyword"].(string); kw == "" {
			return 400, errBody(400, "BAD_REQUEST", "keyword is required for KEYWORD monitors")
		}
	}
	for id, m := range s.monitors {
		if m["name"] == name && (existing == nil || float64(id) != existing["id"]) {
			return 409, errBody(409, "CONFLICT", "Monitor with this name already exists")
		}
	}
	// Omitted nested objects keep the stored ones (mergeMissingAssociationsForUpdate).
	if existing != nil {
		for _, k := range []string{"request", "notification", "alarm", "authentication", "script", "domain"} {
			if in[k] == nil && existing[k] != nil {
				in[k] = deepCopy(existing[k])
			}
		}
		if req, ok := in["request"].(map[string]any); ok {
			if old, ok := existing["request"].(map[string]any); ok {
				if h, ok := req["headers"].(map[string]any); ok {
					oldH, _ := old["headers"].(map[string]any)
					for k, v := range h {
						if v == mask {
							if oldH != nil && oldH[k] != nil {
								h[k] = oldH[k]
							} else {
								delete(h, k)
							}
						}
					}
				}
			}
		}
	}
	if req, ok := in["request"].(map[string]any); ok && req["id"] == nil {
		s.nextRow++
		req["id"] = float64(s.nextRow)
	}
	if n, ok := in["notification"].(map[string]any); ok && n["id"] == nil {
		n["id"] = in["id"]
	}
	// Status: a new monitor starts ACTIVE, an edit without a status keeps the stored one.
	if in["status"] == nil {
		if existing != nil && existing["status"] != nil {
			in["status"] = existing["status"]
		} else {
			in["status"] = "ACTIVE"
		}
	}
	// Interval: the plan minimum when omitted.
	if in["interval"] == nil {
		if existing != nil && existing["interval"] != nil {
			in["interval"] = existing["interval"]
		} else {
			in["interval"] = float64(max(60, s.MinInterval))
		}
	}
	if iv, ok := in["interval"].(float64); ok && s.MinInterval > 0 && int(iv) < s.MinInterval {
		return 400, map[string]any{"code": 50020, "message": fmt.Sprintf("Plan Free enforces a minimum check interval of %d seconds.", s.MinInterval)}
	}
	if typ == "HTTPS" && in["port"] == nil {
		if strings.HasPrefix(url, "https://") {
			in["port"] = float64(443)
		} else {
			in["port"] = float64(80)
		}
	}
	if typ == "HEARTBEAT" {
		if in["heartbeatGraceSeconds"] == nil {
			in["heartbeatGraceSeconds"] = float64(60)
		}
		if existing != nil && existing["heartbeatKey"] != nil {
			in["heartbeatKey"] = existing["heartbeatKey"]
		} else {
			in["heartbeatKey"] = fmt.Sprintf("hb%dkey", s.nextID+1)
		}
	} else {
		delete(in, "heartbeatGraceSeconds")
		delete(in, "heartbeatKey")
	}
	if dt, ok := in["dnsRecordType"].(string); ok {
		in["dnsRecordType"] = strings.ToUpper(dt)
	}
	// Location targeting: one city without a target type becomes a CITY target.
	if locs, ok := in["locations"].([]any); ok {
		for _, l := range locs {
			if lm, ok := l.(map[string]any); ok {
				if city := cityByID(lm["id"]); city != nil {
					lm["name"] = city["name"]
					lm["country"] = city["country"]
				}
			}
		}
		if in["locationTargetType"] == nil && len(locs) == 1 {
			in["locationTargetType"] = "CITY"
			in["locationTargetValue"] = fmt.Sprintf("%.0f", locs[0].(map[string]any)["id"])
		}
	} else {
		in["locations"] = []any{}
	}
	if in["tags"] == nil {
		in["tags"] = []any{}
	}
	if _, ok := in["trackConfigChanges"]; !ok {
		in["trackConfigChanges"] = false
	}
	return 0, nil
}

// masked is the monitor as the API serialises it: credential header values masked.
func masked(m map[string]any) map[string]any {
	out := deepCopy(m)
	delete(out, "heartbeatKey")
	if req, ok := out["request"].(map[string]any); ok {
		if h, ok := req["headers"].(map[string]any); ok {
			for k := range h {
				if secretHeader.MatchString(k) {
					h[k] = mask
				}
			}
		}
	}
	return out
}

func deepCopy[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAny(w http.ResponseWriter, status int, v any) { writeJSON(w, status, v) }

func errBody(code int, status, msg string) map[string]any {
	return map[string]any{"timestamp": "29-10-2026 10:00:00", "code": code, "status": status, "message": msg}
}

func writeErr(w http.ResponseWriter, code int, status, msg string) {
	writeJSON(w, code, errBody(code, status, msg))
}

func writePlanLimit(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"code": 50020, "message": msg})
}

var locationTree = []map[string]any{
	{"type": "CONTINENT", "value": "Europe", "label": "Europe", "children": []map[string]any{
		{"type": "COUNTRY", "value": "82", "label": "Germany", "children": []map[string]any{
			{"type": "CITY", "value": "2925533", "label": "Frankfurt am Main", "children": []any{}},
		}},
		{"type": "COUNTRY", "value": "232", "label": "United Kingdom", "children": []map[string]any{
			{"type": "CITY", "value": "2643743", "label": "London", "children": []any{}},
		}},
	}},
	{"type": "CONTINENT", "value": "Americas", "label": "Americas", "children": []map[string]any{
		{"type": "COUNTRY", "value": "233", "label": "United States", "children": []map[string]any{
			{"type": "CITY", "value": "4140963", "label": "Washington", "children": []any{}},
			{"type": "CITY", "value": "5368361", "label": "Los Angeles", "children": []any{}},
		}},
	}},
}

func cityByID(v any) map[string]any {
	id := fmt.Sprintf("%.0f", v)
	for _, cont := range locationTree {
		for _, country := range cont["children"].([]map[string]any) {
			for _, city := range country["children"].([]map[string]any) {
				if city["value"] == id {
					return map[string]any{"name": city["label"], "country": map[string]any{"name": country["label"]}}
				}
			}
		}
	}
	return nil
}
