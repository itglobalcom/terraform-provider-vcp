# What each vStack resource has been charged this month so far.
data "vcp_project_consumption_resources" "vstack" {
  service = "vstack"
}

# Every resource charged in October, largest amount first.
data "vcp_project_consumption_resources" "october" {
  from = "2026-10-01"
  to   = "2026-10-31"
}

# id and name are null for resources that have none, hence the fallbacks.
output "october_by_resource" {
  value = [
    for r in data.vcp_project_consumption_resources.october.resources :
    "${r.type} ${coalesce(r.name, r.id, "unnamed")}: ${r.total}"
  ]
}
