# Access keys are imported as <group_id>/<access_key_id>. The secret is not
# imported: the API hands it out once, when the key is issued.
terraform import vcp_llm_access_key.api 42/1001
