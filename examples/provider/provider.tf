terraform {
  required_providers {
    uplapse = {
      source  = "uplapse/uplapse"
      version = "~> 0.1"
    }
  }
}

# The API key is best passed through the UPLAPSE_API_KEY environment variable.
provider "uplapse" {
  # api_key  = var.uplapse_api_key
  # endpoint = "https://api.uplapse.com"
}
