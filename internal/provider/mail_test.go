package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/uplapse/terraform-provider-uplapse/internal/mockapi"
)

func TestMailMonitors(t *testing.T) {
	m, provider := newMock(t)
	var imapID int64

	create := provider + `
resource "uplapse_monitor" "imap" {
  name          = "Mailbox"
  type          = "MAIL_SERVER"
  url           = "imap.example.com"
  mail_protocol = "IMAPS"
}

resource "uplapse_monitor" "relay" {
  name          = "Relay"
  type          = "MAIL_SERVER"
  url           = "smtp.example.com"
  mail_protocol = "SUBMISSION"
  port          = 2525
}

resource "uplapse_monitor" "auth" {
  name                  = "Email authentication"
  type                  = "MAIL_AUTH"
  url                   = "example.com"
  required_mta_sts_mode = "enforce"
  check_blacklists      = true
}

data "uplapse_monitor" "auth" {
  id = uplapse_monitor.auth.id
}
`
	// The mailbox moves to SMTP submission with no port given (the default must follow the protocol), and the
	// authentication monitor stops requiring MTA-STS and watching blacklists.
	update := provider + `
resource "uplapse_monitor" "imap" {
  name          = "Mailbox"
  type          = "MAIL_SERVER"
  url           = "imap.example.com"
  mail_protocol = "SUBMISSION"
}

resource "uplapse_monitor" "relay" {
  name          = "Relay"
  type          = "MAIL_SERVER"
  url           = "smtp.example.com"
  mail_protocol = "SUBMISSION"
  port          = 2525
}

resource "uplapse_monitor" "auth" {
  name = "Email authentication"
  type = "MAIL_AUTH"
  url  = "example.com"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		CheckDestroy:             checkDestroyed(m),
		Steps: []resource.TestStep{
			{
				Config: create,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("uplapse_monitor.imap", "mail_protocol", "IMAPS"),
					resource.TestCheckResourceAttr("uplapse_monitor.imap", "port", "993"),
					resource.TestCheckResourceAttr("uplapse_monitor.imap", "check_blacklists", "false"),
					resource.TestCheckNoResourceAttr("uplapse_monitor.imap", "required_mta_sts_mode"),
					resource.TestCheckResourceAttr("uplapse_monitor.relay", "port", "2525"),
					resource.TestCheckResourceAttr("uplapse_monitor.auth", "required_mta_sts_mode", "enforce"),
					resource.TestCheckResourceAttr("uplapse_monitor.auth", "check_blacklists", "true"),
					resource.TestCheckNoResourceAttr("uplapse_monitor.auth", "mail_protocol"),
					resource.TestCheckResourceAttr("data.uplapse_monitor.auth", "required_mta_sts_mode", "enforce"),
					resource.TestCheckResourceAttr("data.uplapse_monitor.auth", "check_blacklists", "true"),
					func(s *terraform.State) error {
						var err error
						if imapID, err = stateID(s, "uplapse_monitor.imap"); err != nil {
							return err
						}
						if got := m.Monitor(imapID)["mailProtocol"]; got != "IMAPS" {
							return fmt.Errorf("stored mailProtocol = %v, want IMAPS", got)
						}
						return nil
					},
				),
			},
			{Config: create, PlanOnly: true},
			{
				// A login set in the app (not modelled here) must survive the protocol change.
				PreConfig: func() {
					m.Mutate(imapID, func(mon map[string]any) {
						mon["authentication"] = map[string]any{"id": float64(9), "type": "BASIC", "username": "probe"}
					})
				},
				Config: update,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectUnknownValue("uplapse_monitor.imap", tfjsonPath("port")),
					plancheck.ExpectResourceAction("uplapse_monitor.relay", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("uplapse_monitor.imap", "mail_protocol", "SUBMISSION"),
					resource.TestCheckResourceAttr("uplapse_monitor.imap", "port", "587"),
					resource.TestCheckNoResourceAttr("uplapse_monitor.auth", "required_mta_sts_mode"),
					resource.TestCheckResourceAttr("uplapse_monitor.auth", "check_blacklists", "false"),
					func(*terraform.State) error {
						stored := m.Monitor(imapID)
						if stored["port"] != float64(587) {
							return fmt.Errorf("stored port = %v, want 587", stored["port"])
						}
						if stored["authentication"] == nil {
							return fmt.Errorf("the login set in the app was lost")
						}
						return nil
					},
				),
			},
			{Config: update, PlanOnly: true},
		},
	})
}

// An HTTPS monitor that moves from http:// to https:// without a port gets 443, not the stored 80.
func TestPortFollowsURLScheme(t *testing.T) {
	m, provider := newMock(t)
	cfg := func(url string) string {
		return provider + fmt.Sprintf(`
resource "uplapse_monitor" "site" {
  name = "Site"
  type = "HTTPS"
  url  = %q
}
`, url)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviders,
		CheckDestroy:             checkDestroyed(m),
		Steps: []resource.TestStep{
			{Config: cfg("http://example.com"), Check: resource.TestCheckResourceAttr("uplapse_monitor.site", "port", "80")},
			{
				// Same scheme, new host: the port stays known in the plan.
				Config: cfg("http://www.example.com"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectKnownValue("uplapse_monitor.site", tfjsonPath("port"), knownInt(80)),
				}},
			},
			{Config: cfg("https://www.example.com"), Check: resource.TestCheckResourceAttr("uplapse_monitor.site", "port", "443")},
			{Config: cfg("https://www.example.com"), PlanOnly: true},
		},
	})
}

func TestMailValidation(t *testing.T) {
	_, provider := newMock(t)
	cases := map[string]string{
		"mail_protocol is required": `type = "MAIL_SERVER"
  url = "imap.example.com"`,
		"Port 25 cannot be monitored": `type = "MAIL_SERVER"
  url = "mx.example.com"
  mail_protocol = "SUBMISSION"
  port = 25`,
		"mail_protocol only applies": `type = "HTTPS"
  url = "https://example.com"
  mail_protocol = "IMAPS"`,
		"required_mta_sts_mode only applies": `type = "DNS"
  url = "example.com"
  required_mta_sts_mode = "testing"`,
		"check_blacklists only applies": `type = "PING"
  url = "example.com"
  check_blacklists = true`,
		`value must be one of`: `type = "MAIL_SERVER"
  url = "mx.example.com"
  mail_protocol = "SMTP"`,
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

// The mock refuses what the API refuses, so the provider's plan-time checks and the API agree.
func TestMockMailRules(t *testing.T) {
	s := mockapi.New(testKey)
	for _, in := range []map[string]any{
		{"name": "a", "type": "MAIL_SERVER", "url": "mx.example.com", "mailProtocol": "SMTP"},
		{"name": "b", "type": "MAIL_SERVER", "url": "mx.example.com", "mailProtocol": "SUBMISSION", "port": float64(25)},
		{"name": "c", "type": "MAIL_AUTH", "url": "example.com", "requiredMtaStsMode": "strict"},
	} {
		if code, _ := s.Validate(in); code != 400 {
			t.Errorf("%v: got %d, want 400", in, code)
		}
	}
}
