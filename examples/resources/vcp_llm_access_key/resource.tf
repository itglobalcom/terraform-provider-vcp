resource "vcp_llm_access_key" "api" {
  group_id = vcp_llm_group.backend.id
  name     = "api"
  tags     = ["production"]
}

# A key kept, but refused by the models until it is unlocked.
resource "vcp_llm_access_key" "reserve" {
  group_id    = vcp_llm_group.backend.id
  name        = "reserve"
  locked      = true
  lock_reason = "Issued for the failover; unlock when switching over."
}

output "api_key_secret" {
  value     = vcp_llm_access_key.api.secret
  sensitive = true
}
