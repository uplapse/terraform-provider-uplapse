package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "upm_key")
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestNewValidates(t *testing.T) {
	if _, err := New("https://api.example.com", ""); err == nil {
		t.Error("expected an error without an API key")
	}
	if _, err := New("ftp://api.example.com", "k"); err == nil {
		t.Error("expected an error for a non-http endpoint")
	}
	c, err := New("", "k")
	if err != nil || c.Endpoint() != DefaultEndpoint {
		t.Errorf("empty endpoint should default to %s, got %v %v", DefaultEndpoint, c, err)
	}
	c, _ = New("https://api.example.com/", "k")
	if c.Endpoint() != "https://api.example.com" {
		t.Errorf("trailing slash not trimmed: %s", c.Endpoint())
	}
}

func TestSendsAPIKeyAndDecodes(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "upm_key" {
			t.Errorf("missing API key header")
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/monitor/7" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":7,"name":"site","type":"HTTPS","url":"https://example.com","port":443,
			"status":"ACTIVE","locations":[{"id":2925533,"name":"Frankfurt am Main"}],"tags":["prod"],
			"date":"2026-10-29T10:00:00","checkHeaderHasValue":false}`)
	})
	m, err := c.GetMonitor(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != 7 || m.Name != "site" || *m.Port != 443 || m.Locations[0].ID != 2925533 || m.Tags[0] != "prod" {
		t.Errorf("decoded %+v", m)
	}
}

func TestErrorMessagesSurfaceTheAPIExplanation(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       []string
	}{
		{"plan limit", `{"code":50020,"message":"Plan Free enforces a minimum check interval of 300 seconds."}`, 400,
			[]string{"400", "50020", "minimum check interval of 300 seconds"}},
		{"validation", `{"timestamp":"x","code":400,"status":"BAD_REQUEST","message":"url is required for HTTPS monitors"}`, 400,
			[]string{"url is required for HTTPS monitors"}},
		{"monitor down", `{"code":"MONITOR_DOWN","status":"CONFLICT","message":"The monitor is down"}`, 409,
			[]string{"MONITOR_DOWN", "The monitor is down"}},
		{"forbidden", `{"code":403,"status":"FORBIDDEN","message":"Access Denied"}`, 403,
			[]string{"Access Denied", "EDITOR"}},
		{"waf html", `<!DOCTYPE html><html>blocked</html>`, 403, []string{"firewall"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := c.CreateMonitor(context.Background(), &MonitorInput{Name: "a", Type: "HTTPS"})
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	_, err := c.GetMonitor(context.Background(), 1)
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := c.DeleteMonitor(context.Background(), 1); err != nil {
		t.Fatalf("deleting a missing monitor should succeed, got %v", err)
	}
}

func TestRetriesRateLimit(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":"rate_limited"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":1,"name":"a","type":"PING","status":"ACTIVE"}`)
	})
	if _, err := c.CreateMonitor(context.Background(), &MonitorInput{Name: "a", Type: "PING"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", calls.Load())
	}
}

func TestDoesNotRetryFailedWrites(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := c.CreateMonitor(context.Background(), &MonitorInput{Name: "a", Type: "PING"}); err == nil {
		t.Fatal("expected an error")
	}
	if calls.Load() != 1 {
		t.Errorf("a POST that met a gateway error must not be repeated, got %d calls", calls.Load())
	}
}

func TestCreateSendsNullsForClearedFields(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"id":3,"name":"a","type":"PING","status":"ACTIVE"}`)
	})
	if _, err := c.CreateMonitor(context.Background(), &MonitorInput{Name: "a", Type: "PING", Locations: []City{{ID: 5}}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["keyword"]; !ok || body["keyword"] != nil {
		t.Errorf("keyword should be sent as null, got %v", body["keyword"])
	}
	if _, ok := body["interval"]; ok {
		t.Errorf("an unset interval should be left to the API")
	}
	if _, ok := body["request"]; ok {
		t.Errorf("an unmanaged request should not be sent")
	}
	if _, ok := body["agentMonitor"]; ok {
		t.Errorf("an unset agentMonitor should be left to the API")
	}
}

func TestCreateSendsAgentHeartbeatFields(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"id":4,"name":"agent","type":"HEARTBEAT","status":"ACTIVE","agentMonitor":true,
			"agentMaxCostPerHour":2.5,"agentMaxConsecutiveFailures":3,"agentCostCurrency":"USD"}`)
	})
	agent, cost, failures, currency := true, 2.5, int64(3), "USD"
	m, err := c.CreateMonitor(context.Background(), &MonitorInput{
		Name: "agent", Type: "HEARTBEAT", AgentMonitor: &agent, AgentMaxCostPerHour: &cost,
		AgentMaxConsecutiveFailures: &failures, AgentCostCurrency: &currency,
	})
	if err != nil {
		t.Fatal(err)
	}
	if body["agentMonitor"] != true || body["agentMaxCostPerHour"] != 2.5 || body["agentMaxConsecutiveFailures"] != float64(3) || body["agentCostCurrency"] != "USD" {
		t.Errorf("agent fields not sent as the API names them: %v", body)
	}
	if m.AgentMonitor == nil || !*m.AgentMonitor || m.AgentMaxCostPerHour == nil || *m.AgentMaxCostPerHour != 2.5 ||
		m.AgentMaxConsecutiveFailures == nil || *m.AgentMaxConsecutiveFailures != 3 || m.AgentCostCurrency == nil || *m.AgentCostCurrency != "USD" {
		t.Errorf("agent fields not read back: %+v", m)
	}
}

// UpdateMonitor reads the stored monitor and overlays the input: unmodelled settings and nested row ids survive.
func TestUpdateMergesOverStoredMonitor(t *testing.T) {
	var put map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"id":9,"name":"old","type":"API","url":"https://x.test","interval":60,
				"minTlsDaysLeft":14,"tlsIssuerContains":"Let's Encrypt","date":"2026-01-01T00:00:00",
				"alarm":{"id":44,"conditions":[]},
				"request":{"id":31,"method":"GET","headers":{"Authorization":"********"},"userAgent":"ua"},
				"notification":{"id":9,"users":[{"id":1}],"groups":[{"id":2}]}}`)
		case http.MethodPut:
			if r.URL.Path != "/v1/monitor/9" {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			_ = json.NewDecoder(r.Body).Decode(&put)
			_, _ = io.WriteString(w, `{"id":9,"name":"new","type":"API","status":"ACTIVE"}`)
		}
	})
	method := "POST"
	groups := []Ref{{ID: 8}}
	_, err := c.UpdateMonitor(context.Background(), 9, &MonitorInput{
		Name: "new", Type: "API", URL: strp("https://x.test"),
		Request:      &RequestDetails{Method: &method, Headers: map[string]string{"Authorization": "Bearer s3cret"}},
		Notification: &NotificationInput{Groups: &groups},
	})
	if err != nil {
		t.Fatal(err)
	}
	if put["name"] != "new" || put["tlsIssuerContains"] != "Let's Encrypt" || put["alarm"] == nil {
		t.Errorf("unmodelled settings lost: %v", put)
	}
	if _, ok := put["date"]; ok {
		t.Error("read-only fields should not be sent back")
	}
	if put["minTlsDaysLeft"] != nil {
		t.Error("a managed field left unset must be cleared")
	}
	req := put["request"].(map[string]any)
	if req["id"] != float64(31) || req["method"] != "POST" || req["headers"].(map[string]any)["Authorization"] != "Bearer s3cret" {
		t.Errorf("request merge wrong: %v", req)
	}
	n := put["notification"].(map[string]any)
	if n["id"] != float64(9) || len(n["users"].([]any)) != 1 || n["groups"].([]any)[0].(map[string]any)["id"] != float64(8) {
		t.Errorf("notification merge wrong: %v", n)
	}
}

func TestListLocationsFlattensTree(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/location-target/tree" || r.URL.Query().Get("providers") != "aws,azure" {
			t.Errorf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `[{"type":"CONTINENT","value":"Europe","label":"Europe","children":[
			{"type":"COUNTRY","value":"82","label":"Germany","children":[
				{"type":"CITY","value":"2950159","label":"Berlin","children":[]},
				{"type":"CITY","value":"2925533","label":"Frankfurt am Main","children":[]}]}]},
			{"type":"CONTINENT","value":"Americas","label":"Americas","children":[
			{"type":"COUNTRY","value":"233","label":"United States","children":[
				{"type":"CITY","value":"4140963","label":"Washington","children":[]}]}]}]`)
	})
	locs, err := c.ListLocations(context.Background(), []string{"aws", "azure"})
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 3 || locs[0].Name != "Washington" || locs[0].Continent != "Americas" || locs[1].ID != 2950159 || locs[1].CountryID != 82 {
		t.Errorf("unexpected locations %+v", locs)
	}
}

func TestHeartbeatURL(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"monitorId":4,"heartbeatKey":"abc","pingPath":"/v1/heartbeat/abc"}`)
	})
	h, err := c.GetHeartbeat(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.HeartbeatURL(h); got != c.Endpoint()+"/v1/heartbeat/abc" {
		t.Errorf("heartbeat url %s", got)
	}
}

func strp(s string) *string { return &s }
