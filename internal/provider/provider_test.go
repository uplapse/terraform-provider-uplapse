package provider

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/uplapse/terraform-provider-uplapse/internal/mockapi"
)

const testKey = "upm_test_key"

var testProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"uplapse": providerserver.NewProtocol6WithError(New("test")()),
}

func newMock(t *testing.T) (*mockapi.Server, string) {
	t.Helper()
	m := mockapi.New(testKey)
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return m, fmt.Sprintf(`
provider "uplapse" {
  endpoint = %q
  api_key  = %q
}
`, srv.URL, testKey)
}

func checkDestroyed(m *mockapi.Server) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if ids := m.MonitorIDs(); len(ids) != 0 {
			return fmt.Errorf("monitors left after destroy: %v", ids)
		}
		return nil
	}
}

// stateID returns the monitor id Terraform stored for addr.
func stateID(s *terraform.State, addr string) (int64, error) {
	rs, ok := s.RootModule().Resources[addr]
	if !ok {
		return 0, fmt.Errorf("%s not in state", addr)
	}
	return strconv.ParseInt(rs.Primary.ID, 10, 64)
}

func TestMonitorLifecycle(t *testing.T) {
	m, provider := newMock(t)
	const addr = "uplapse_monitor.site"
	var id int64

	create := provider + `
resource "uplapse_monitor" "site" {
  name      = "Website"
  type      = "HTTPS"
  url       = "https://example.com"
  locations = [2925533, 2643743]
  tags      = ["prod", "web"]
  expected_status = 200
  request = {
    method           = "GET"
    follow_redirects = true
    headers = {
      Authorization = "Bearer s3cret"
      X-Env         = "prod"
    }
  }
}
`
	update := provider + `
resource "uplapse_monitor" "site" {
  name      = "Website (paused)"
  type      = "HTTPS"
  url       = "https://example.com"
  interval  = 120
  timeout   = 30
  locations = [2925533]
  tags      = ["prod"]
  paused    = true
  request = {
    method = "HEAD"
    headers = {
      Authorization = "Bearer s3cret"
      X-Env         = "prod"
    }
  }
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		CheckDestroy:             checkDestroyed(m),
		Steps: []resource.TestStep{
			{
				Config: create,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(addr, "id"),
					resource.TestCheckResourceAttr(addr, "port", "443"),
					resource.TestCheckResourceAttr(addr, "interval", "60"),
					resource.TestCheckResourceAttr(addr, "status", "ACTIVE"),
					resource.TestCheckResourceAttr(addr, "paused", "false"),
					resource.TestCheckResourceAttr(addr, "locations.#", "2"),
					resource.TestCheckResourceAttr(addr, "tags.#", "2"),
					resource.TestCheckNoResourceAttr(addr, "location_target_type"),
					resource.TestCheckNoResourceAttr(addr, "heartbeat_url"),
					resource.TestCheckResourceAttr(addr, "request.headers.Authorization", "Bearer s3cret"),
					func(s *terraform.State) error {
						var err error
						if id, err = stateID(s, addr); err != nil {
							return err
						}
						stored := m.Monitor(id)
						h := stored["request"].(map[string]any)["headers"].(map[string]any)
						if h["Authorization"] != "Bearer s3cret" {
							return fmt.Errorf("the API should store the real header value, got %v", h["Authorization"])
						}
						return nil
					},
				),
			},
			{
				// Settings made in the app that the provider does not model must survive an update.
				PreConfig: func() {
					m.Mutate(id, func(mon map[string]any) {
						mon["alarm"] = map[string]any{"id": float64(77), "conditions": []any{}}
						mon["tlsIssuerContains"] = "R3"
					})
				},
				Config: update,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue(addr, tfjsonPath("status"), knownString("SUSPENDED")),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", "Website (paused)"),
					resource.TestCheckResourceAttr(addr, "status", "SUSPENDED"),
					resource.TestCheckResourceAttr(addr, "interval", "120"),
					resource.TestCheckResourceAttr(addr, "locations.#", "1"),
					resource.TestCheckNoResourceAttr(addr, "location_target_type"),
					resource.TestCheckNoResourceAttr(addr, "expected_status"),
					resource.TestCheckResourceAttr(addr, "request.method", "HEAD"),
					func(*terraform.State) error {
						stored := m.Monitor(id)
						if stored["alarm"] == nil || stored["tlsIssuerContains"] != "R3" {
							return fmt.Errorf("unmodelled settings were erased: %v", stored)
						}
						if stored["status"] != "SUSPENDED" {
							return fmt.Errorf("status %v", stored["status"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:      addr,
				ImportState:       true,
				ImportStateVerify: true,
				// Credential headers come back masked; import cannot know them.
				ImportStateVerifyIgnore: []string{"request.headers.Authorization"},
			},
			{
				// Drift: renamed in the app. The plan must put it back.
				PreConfig: func() {
					m.Mutate(id, func(mon map[string]any) { mon["name"] = "Renamed in the app"; mon["interval"] = float64(600) })
				},
				Config: update,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate),
				}},
				Check: func(*terraform.State) error {
					if got := m.Monitor(id)["name"]; got != "Website (paused)" {
						return fmt.Errorf("drift not corrected, name %v", got)
					}
					return nil
				},
			},
			{
				// Deleted in the app: Terraform creates it again.
				PreConfig: func() { m.Remove(id) },
				Config:    update,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate),
				}},
				Check: func(s *terraform.State) error {
					newID, err := stateID(s, addr)
					if err != nil {
						return err
					}
					if newID == id || m.Monitor(newID) == nil {
						return fmt.Errorf("expected a new monitor, got %d", newID)
					}
					return nil
				},
			},
			{
				Config:   update,
				PlanOnly: true, // nothing left to change
			},
		},
	})
}

func TestMonitorTypes(t *testing.T) {
	m, provider := newMock(t)
	cfg := provider + `
resource "uplapse_monitor" "hb" {
  name     = "Nightly backup"
  type     = "HEARTBEAT"
  interval = 86400
}

resource "uplapse_monitor" "dns" {
  name            = "MX record"
  type            = "DNS"
  url             = "example.com"
  dns_record_type = "MX"
  expected_dns    = "10 mail.example.com"
}

resource "uplapse_monitor" "tcp" {
  name        = "SMTP"
  type        = "TCP"
  url         = "mail.example.com"
  port        = 25
  tcp_expect  = "220"
}

resource "uplapse_monitor" "kw" {
  name    = "Shop keyword"
  type    = "KEYWORD"
  url     = "https://shop.example.com"
  path    = "/health"
  keyword = "ok"
  location_target_type  = "COUNTRY"
  location_target_value = "82"
}

resource "uplapse_monitor" "tls" {
  name              = "Certificate"
  type              = "TLS_CERT"
  url               = "example.com"
  min_tls_days_left = 14
  track_config_changes = true
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		CheckDestroy:             checkDestroyed(m),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("uplapse_monitor.hb", "heartbeat_url", regexp.MustCompile(`^http://127\.0\.0\.1:\d+/v1/heartbeat/\w+$`)),
					resource.TestCheckResourceAttr("uplapse_monitor.hb", "heartbeat_grace_seconds", "60"),
					resource.TestCheckNoResourceAttr("uplapse_monitor.hb", "url"),
					resource.TestCheckResourceAttr("uplapse_monitor.dns", "dns_record_type", "MX"),
					resource.TestCheckNoResourceAttr("uplapse_monitor.dns", "port"),
					resource.TestCheckResourceAttr("uplapse_monitor.tcp", "port", "25"),
					resource.TestCheckResourceAttr("uplapse_monitor.kw", "location_target_type", "COUNTRY"),
					resource.TestCheckResourceAttr("uplapse_monitor.tls", "track_config_changes", "true"),
				),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

func TestMonitorAlertRecipients(t *testing.T) {
	m, provider := newMock(t)
	const addr = "uplapse_monitor.api"
	var id int64
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		CheckDestroy:             checkDestroyed(m),
		Steps: []resource.TestStep{
			{
				Config: provider + `
resource "uplapse_monitor" "api" {
  name           = "API"
  type           = "API"
  url            = "https://api.example.com"
  alert_user_ids = [11, 12]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "alert_user_ids.#", "2"),
					resource.TestCheckNoResourceAttr(addr, "alert_contact_group_ids"),
					func(s *terraform.State) error {
						var err error
						id, err = stateID(s, addr)
						return err
					},
				),
			},
			{
				// Groups chosen in the app are kept while the configuration leaves them out.
				PreConfig: func() {
					m.Mutate(id, func(mon map[string]any) {
						mon["notification"].(map[string]any)["groups"] = []any{map[string]any{"id": float64(5)}}
					})
				},
				Config: provider + `
resource "uplapse_monitor" "api" {
  name           = "API"
  type           = "API"
  url            = "https://api.example.com"
  alert_user_ids = []
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "alert_user_ids.#", "0"),
					func(*terraform.State) error {
						n := m.Monitor(id)["notification"].(map[string]any)
						if len(n["users"].([]any)) != 0 || len(n["groups"].([]any)) != 1 {
							return fmt.Errorf("notification %v", n)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestMonitorValidation(t *testing.T) {
	_, provider := newMock(t)
	cases := map[string]string{
		"keyword is required": `type = "KEYWORD"
  url = "https://example.com"`,
		"port is required": `type = "PORT"
  url = "example.com"`,
		"url is required": `type = "PING"`,
		"cannot be longer than the interval": `type = "PING"
  url = "example.com"
  interval = 30
  timeout = 60`,
		`value must be one of`: `type = "WEBSITE"
  url = "example.com"`,
		"must start with http": `type = "HTTPS"
  url = "ftp://example.com"`,
	}
	for want, body := range cases {
		t.Run(want, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProviders,
				Steps: []resource.TestStep{{
					Config:      provider + "resource \"uplapse_monitor\" \"x\" {\n  name = \"x\"\n  " + body + "\n}\n",
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(regexp.QuoteMeta(want)),
				}},
			})
		})
	}
}

func TestMonitorPlanLimitError(t *testing.T) {
	m, provider := newMock(t)
	m.MinInterval = 300
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{{
			Config: provider + `
resource "uplapse_monitor" "x" {
  name     = "fast"
  type     = "PING"
  url      = "example.com"
  interval = 30
}
`,
			ExpectError: regexp.MustCompile(`minimum\s+check\s+interval\s+of\s+300\s+seconds`),
		}},
	})
}

func TestBadAPIKey(t *testing.T) {
	_, provider := newMock(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{{
			Config: regexp.MustCompile(`api_key  = ".*"`).ReplaceAllString(provider, `api_key = "upm_wrong"`) + `
data "uplapse_locations" "all" {}
`,
			ExpectError: regexp.MustCompile(`check the API key`),
		}},
	})
}

func TestDataSources(t *testing.T) {
	m, provider := newMock(t)
	id := m.Put(map[string]any{
		"name": "From the app", "type": "PING", "url": "example.org", "interval": float64(300),
		"status": "SUSPENDED", "locations": []any{map[string]any{"id": float64(4140963)}}, "tags": []any{"edge"},
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{{
			Config: provider + fmt.Sprintf(`
data "uplapse_locations" "all" {}

data "uplapse_locations" "germany" {
  country = "germany"
}

data "uplapse_monitor" "existing" {
  id = "%d"
}
`, id),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.uplapse_locations.all", "locations.#", "4"),
				resource.TestCheckResourceAttr("data.uplapse_locations.germany", "locations.#", "1"),
				resource.TestCheckResourceAttr("data.uplapse_locations.germany", "locations.0.id", "2925533"),
				resource.TestCheckResourceAttr("data.uplapse_locations.germany", "locations.0.name", "Frankfurt am Main"),
				resource.TestCheckResourceAttr("data.uplapse_locations.germany", "locations.0.country_id", "82"),
				resource.TestCheckResourceAttr("data.uplapse_locations.germany", "locations.0.continent", "Europe"),
				resource.TestCheckResourceAttr("data.uplapse_monitor.existing", "name", "From the app"),
				resource.TestCheckResourceAttr("data.uplapse_monitor.existing", "paused", "true"),
				resource.TestCheckResourceAttr("data.uplapse_monitor.existing", "interval", "300"),
				resource.TestCheckResourceAttr("data.uplapse_monitor.existing", "locations.#", "1"),
				resource.TestCheckResourceAttr("data.uplapse_monitor.existing", "tags.0", "edge"),
			),
		}},
	})
}

func TestImportExistingMonitor(t *testing.T) {
	m, provider := newMock(t)
	id := m.Put(map[string]any{
		"name": "Imported", "type": "HTTPS", "url": "https://example.net", "port": float64(443),
		"interval": float64(60), "timeout": float64(30), "status": "ACTIVE", "trackConfigChanges": false,
		"locations": []any{map[string]any{"id": float64(2925533)}}, "locationTargetType": "CITY", "locationTargetValue": "2925533",
		"tags":    []any{},
		"request": map[string]any{"id": float64(1), "method": "GET", "headers": map[string]any{"X-Env": "prod"}},
	})
	cfg := provider + `
resource "uplapse_monitor" "imported" {
  name      = "Imported"
  type      = "HTTPS"
  url       = "https://example.net"
  timeout   = 30
  locations = [2925533]
  request = {
    method  = "GET"
    headers = { X-Env = "prod" }
  }
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		Steps: []resource.TestStep{
			{
				Config:             cfg,
				ResourceName:       "uplapse_monitor.imported",
				ImportState:        true,
				ImportStateId:      strconv.FormatInt(id, 10),
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 || states[0].Attributes["name"] != "Imported" || states[0].Attributes["request.headers.X-Env"] != "prod" {
						return fmt.Errorf("unexpected import %v", states)
					}
					return nil
				},
			},
			{Config: cfg, PlanOnly: true},
			{
				Config:        cfg,
				ResourceName:  "uplapse_monitor.imported",
				ImportState:   true,
				ImportStateId: "not-a-number",
				ExpectError:   regexp.MustCompile(`Invalid import id`),
			},
		},
	})
}
