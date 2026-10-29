data "uplapse_monitor" "existing" {
  id = "123"
}

output "existing_status" {
  value = data.uplapse_monitor.existing.status
}
