package vstack_server_test

import (
	tfjson "github.com/hashicorp/terraform-json"
)

// findResource returns the resource at the given address from a Terraform state,
// or nil if not present. Shared by the acceptance-test state checks.
func findResource(state *tfjson.State, address string) *tfjson.StateResource {
	if state == nil || state.Values == nil || state.Values.RootModule == nil {
		return nil
	}
	for _, r := range state.Values.RootModule.Resources {
		if r.Address == address {
			return r
		}
	}
	return nil
}
