data "vcp_llm_models" "catalog" {
  depends_on = [vcp_llm_connection.this]
}

# A group that may call every text model of the local contour.
resource "vcp_llm_group" "backend" {
  name = "backend"
  model_ids = [
    for m in data.vcp_llm_models.catalog.models : m.id
    if m.kind == "text" && m.contour == "local" && m.is_available_to_groups
  ]
  tags = ["production"]
}
