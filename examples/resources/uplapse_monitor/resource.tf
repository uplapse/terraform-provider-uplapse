data "uplapse_locations" "europe" {
  continent = "Europe"
}

# Website: HTTPS check from every European location.
resource "uplapse_monitor" "website" {
  name            = "Website"
  type            = "HTTPS"
  url             = "https://www.example.com"
  interval        = 60
  timeout         = 30
  expected_status = 200
  locations       = data.uplapse_locations.europe.locations[*].id
  tags            = ["prod", "web"]
}

# API with request settings. Header values are sensitive and stored encrypted.
resource "uplapse_monitor" "api" {
  name     = "Orders API"
  type     = "API"
  url      = "https://api.example.com"
  path     = "/v1/health"
  interval = 60

  request = {
    method           = "GET"
    follow_redirects = false
    verify_ssl       = true
    headers = {
      Authorization = "Bearer ${var.health_token}"
    }
  }

  alert_contact_group_ids = [12]
}

# The page must contain a word.
resource "uplapse_monitor" "shop" {
  name    = "Shop"
  type    = "KEYWORD"
  url     = "https://shop.example.com"
  keyword = "Add to cart"
}

resource "uplapse_monitor" "ping" {
  name = "Edge router"
  type = "PING"
  url  = "edge.example.com"
}

resource "uplapse_monitor" "smtp" {
  name       = "Mail server"
  type       = "TCP"
  url        = "mail.example.com"
  port       = 25
  tcp_expect = "220"
}

resource "uplapse_monitor" "postgres_port" {
  name   = "Database port"
  type   = "PORT"
  url    = "db.example.com"
  port   = 5432
  paused = true
}

resource "uplapse_monitor" "mx" {
  name            = "MX record"
  type            = "DNS"
  url             = "example.com"
  dns_record_type = "MX"
  expected_dns    = "10 mail.example.com"
}

resource "uplapse_monitor" "certificate" {
  name                 = "Certificate"
  type                 = "TLS_CERT"
  url                  = "www.example.com"
  min_tls_days_left    = 14
  interval             = 3600
  track_config_changes = true
}

resource "uplapse_monitor" "domain" {
  name                 = "Domain registration"
  type                 = "DOMAIN_EXPIRY"
  url                  = "example.com"
  min_domain_days_left = 30
  interval             = 86400
}

# The domain's email authentication: SPF and DMARC, plus MTA-STS in enforce mode and an IP blacklist watch on its
# mail servers.
resource "uplapse_monitor" "email_auth" {
  name                  = "Email authentication"
  type                  = "MAIL_AUTH"
  url                   = "example.com"
  required_mta_sts_mode = "enforce"
  check_blacklists      = true
  interval              = 3600
}

# A mailbox server: TLS on IMAPS (port 993 by default) and its certificate. Add a username and password in the app
# (Advanced > Authentication) to check that logging in works too.
resource "uplapse_monitor" "imap" {
  name          = "Mailbox server"
  type          = "MAIL_SERVER"
  url           = "imap.example.com"
  mail_protocol = "IMAPS"
}

# A job that must report in once a day: call heartbeat_url when it finishes.
resource "uplapse_monitor" "backup" {
  name                    = "Nightly backup"
  type                    = "HEARTBEAT"
  interval                = 86400
  heartbeat_grace_seconds = 1800
}

output "backup_ping_url" {
  value     = uplapse_monitor.backup.heartbeat_url
  sensitive = true
}

variable "health_token" {
  type      = string
  sensitive = true
}
