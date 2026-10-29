# Terraform provider for Uplapse

Manage [Uplapse](https://uplapse.com) uptime monitors with Terraform.

```hcl
terraform {
  required_providers {
    uplapse = { source = "uplapse/uplapse" }
  }
}

provider "uplapse" {} # UPLAPSE_API_KEY from the environment

data "uplapse_locations" "europe" {
  continent = "Europe"
}

resource "uplapse_monitor" "website" {
  name      = "Website"
  type      = "HTTPS"
  url       = "https://www.example.com"
  locations = data.uplapse_locations.europe.locations[*].id
}
```

| | |
|---|---|
| Resource `uplapse_monitor` | create, update, delete, import by id; drift is read back |
| Data source `uplapse_locations` | cities monitors can run from (filter by continent, country, cloud) |
| Data source `uplapse_monitor` | one monitor by id |

Full reference: [`docs/`](docs/) (generated, registry layout). Examples: [`examples/`](examples/).

## Authentication

Create an API key under **Settings > API keys** in the app and pass it as `api_key` or `UPLAPSE_API_KEY`. The key's
roles decide what Terraform may do: managing monitors needs `OWNER`, `DOMAIN_ADMIN` or `EDITOR`; reading needs any
role. `endpoint` (or `UPLAPSE_ENDPOINT`) defaults to `https://api.uplapse.com`.

## How updates behave

The API replaces a monitor with what it is sent. So that settings made in the app and not modelled here (alert
conditions, authentication, TLS assertions, journeys...) survive `terraform apply`, an update reads the stored
monitor, changes the attributes the configuration manages and sends the whole monitor back, as the web app's editor
does. The `request` block and the `alert_user_ids` / `alert_contact_group_ids` sets are only managed when present in
the configuration; leaving them out keeps what was chosen in the app.

Credential-bearing request header values (Authorization, tokens, cookies, keys) are stored encrypted and never
returned by the API, so a change to one of them made outside Terraform cannot be detected.

## Development

```sh
go build ./...
go vet ./...
go test ./...          # client tests + resource tests (terraform on PATH, mock API, no network)
go generate ./...      # regenerate docs/ with tfplugindocs
```

The resource tests run real Terraform against `internal/mockapi`, an in-memory imitation of the API endpoints the
provider calls. To try the provider by hand without an account:

```sh
go build -o ./bin/terraform-provider-uplapse .
go run ./internal/mockapi/cmd/mockserver -addr 127.0.0.1:18080 -key upm_test_key &

cat > dev.tfrc <<EOF
provider_installation {
  dev_overrides { "uplapse/uplapse" = "$PWD/bin" }
  direct {}
}
EOF
export TF_CLI_CONFIG_FILE=$PWD/dev.tfrc UPLAPSE_API_KEY=upm_test_key UPLAPSE_ENDPOINT=http://127.0.0.1:18080
terraform plan   # in a directory with a configuration; no `terraform init` with dev_overrides
```

API endpoints used:

| Call | Endpoint |
|---|---|
| create | `POST /v1/monitor/save` |
| read / import | `GET /v1/monitor/{id}` |
| update | `GET` then `PUT /v1/monitor/{id}` |
| delete | `DELETE /v1/monitor/{id}` |
| heartbeat URL | `GET /v1/monitor/{id}/heartbeat` |
| locations | `GET /v1/location-target/tree` |

## Publishing

The Terraform Registry only publishes from a **public GitHub repository**. What the owner has to do once:

1. **GitHub repository.** Create a public repository named exactly `terraform-provider-uplapse` under the GitHub
   organisation or user that will own the namespace (`uplapse` gives the source address `uplapse/uplapse`; another
   owner changes the address and the `Address` in `main.go`). Push this repository's `main` branch to it. If the
   module path should follow the owner, change `github.com/uplapse/terraform-provider-uplapse` in `go.mod` and imports.
2. **Licence.** Add a `LICENSE` file (HashiCorp's scaffolding uses MPL-2.0); none is chosen here.
3. **GPG signing key.** Generate an RSA or DSA key (the registry does not accept ECC keys):
   `gpg --full-generate-key` (RSA 4096), then export the private key `gpg --armor --export-secret-keys <KEY_ID>`
   and the public key `gpg --armor --export <KEY_ID>`. Keep the private key and passphrase in the password store.
4. **GitHub secrets** (repository > Settings > Secrets and variables > Actions):
   - `GPG_PRIVATE_KEY`: the ASCII-armoured private key;
   - `PASSPHRASE`: its passphrase.
   `GITHUB_TOKEN` is provided by Actions; `.github/workflows/release.yml` already uses all three.
5. **Registry sign-in.** Sign in to <https://registry.terraform.io> with the GitHub account that owns the repository,
   authorise the Terraform Registry GitHub app for that organisation, then under **User Settings > Signing Keys** add
   the GPG **public** key for the namespace.
6. **Publish.** In the registry: **Publish > Provider**, pick the repository. Then tag a release:
   `git tag v0.1.0 && git push origin v0.1.0`. The release workflow runs GoReleaser (`.goreleaser.yml`), which builds,
   zips, writes the `SHA256SUMS` and the `_manifest.json` (from `terraform-registry-manifest.json`, protocol 6.0) and
   signs the sums; the registry picks the release up through its webhook. Later versions only need a new `v*` tag.

Before the first public release, check that the API is reachable for customers: the stage-1 Cloudflare WAF on
`api.uplapse.com` only lets `/public/*` and `/v1/subscription/plans` through, which blocks every endpoint above.

The GitLab pipeline (`.gitlab-ci.yml`) runs `go vet` and `go test` on GitLab; the GitHub workflows (`test.yml`,
`release.yml`) are for the public mirror.
