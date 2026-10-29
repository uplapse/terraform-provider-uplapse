# Every location.
data "uplapse_locations" "all" {}

# Cities in one country, from AWS and Azure only.
data "uplapse_locations" "germany" {
  country   = "Germany"
  providers = ["aws", "azure"]
}

output "location_ids" {
  value = { for l in data.uplapse_locations.all.locations : "${l.name}, ${l.country}" => l.id }
}
