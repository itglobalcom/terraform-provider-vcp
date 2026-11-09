# Connect the project to the LLM service. On a project that is already
# connected the existing connection is taken into the state as is.
resource "vcp_llm_connection" "this" {
  group_name     = "default"
  first_key_name = "default"
}
