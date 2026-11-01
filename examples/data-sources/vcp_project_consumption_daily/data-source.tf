# What the project was charged on each day of October.
data "vcp_project_consumption_daily" "october" {
  from = "2026-10-01"
  to   = "2026-10-31"
}

output "daily" {
  value = { for d in data.vcp_project_consumption_daily.october.days : d.date => d.total }
}
