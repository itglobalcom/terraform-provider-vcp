package acctest

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
)

// FindResource returns the resource at the given address from a Terraform
// state, or nil if not present. Shared by custom state checks.
func FindResource(state *tfjson.State, address string) *tfjson.StateResource {
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

// CheckListNotEmpty returns a state check verifying that the attribute is a
// non-empty list.
func CheckListNotEmpty(resourceAddress, attribute string) statecheck.StateCheck {
	return listNotEmptyCheck{resourceAddress: resourceAddress, attribute: attribute}
}

type listNotEmptyCheck struct {
	resourceAddress string
	attribute       string
}

func (c listNotEmptyCheck) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	res := FindResource(req.State, c.resourceAddress)
	if res == nil {
		resp.Error = fmt.Errorf("resource not found: %s", c.resourceAddress)
		return
	}

	attrValue, ok := res.AttributeValues[c.attribute]
	if !ok {
		resp.Error = fmt.Errorf("attribute %s not found in resource %s", c.attribute, c.resourceAddress)
		return
	}

	list, ok := attrValue.([]any)
	if !ok {
		resp.Error = fmt.Errorf("attribute %s is not a list (got type %T)", c.attribute, attrValue)
		return
	}

	if len(list) == 0 {
		resp.Error = fmt.Errorf("attribute %s is an empty list", c.attribute)
	}
}

// ImportIDFunc builds a composite import ID by joining the given state
// attributes of the resource with ":" (e.g. "server_id", "id" →
// "<server_id>:<nic_id>").
func ImportIDFunc(resourceName string, attrs ...string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return "", fmt.Errorf("resource not found: %s", resourceName)
		}
		parts := make([]string, len(attrs))
		for i, attr := range attrs {
			v, ok := rs.Primary.Attributes[attr]
			if !ok || v == "" {
				return "", fmt.Errorf("attribute %q not set on %s", attr, resourceName)
			}
			parts[i] = v
		}
		return strings.Join(parts, ":"), nil
	}
}

// ComposeCheckDestroy runs several CheckDestroy functions in order and
// returns the first error.
func ComposeCheckDestroy(checks ...func(*terraform.State) error) func(*terraform.State) error {
	return func(s *terraform.State) error {
		for _, check := range checks {
			if err := check(s); err != nil {
				return err
			}
		}
		return nil
	}
}

// checkDestroyed verifies that no resource of the given type from the state
// still exists in the API. getByID must return the API error for the ID.
func checkDestroyed(s *terraform.State, resourceType string, getByID func(ctx context.Context, id string) error) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != resourceType {
			continue
		}
		err := getByID(context.Background(), rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("%s %s still exists", resourceType, rs.Primary.ID)
		}
		if !sdk.IsNotFound(err) {
			return fmt.Errorf("error checking %s %s destruction: %w", resourceType, rs.Primary.ID, err)
		}
	}
	return nil
}

// CheckServersDestroyed is a CheckDestroy for vcp_server resources.
func CheckServersDestroyed(s *terraform.State) error {
	return checkDestroyed(s, "vcp_server", func(ctx context.Context, id string) error {
		_, err := GetTestClient().GetServer(ctx, id)
		return err
	})
}

// CheckGatewaysDestroyed is a CheckDestroy for vcp_gateway resources.
func CheckGatewaysDestroyed(s *terraform.State) error {
	return checkDestroyed(s, "vcp_gateway", func(ctx context.Context, id string) error {
		_, err := GetTestClient().GetGateway(ctx, id)
		return err
	})
}

// CheckNetworksDestroyed is a CheckDestroy for vcp_network resources.
func CheckNetworksDestroyed(s *terraform.State) error {
	return checkDestroyed(s, "vcp_network", func(ctx context.Context, id string) error {
		_, err := GetTestClient().GetNetwork(ctx, id)
		return err
	})
}

// CheckAffinityGroupsDestroyed is a CheckDestroy for vcp_affinity_group resources.
func CheckAffinityGroupsDestroyed(s *terraform.State) error {
	return checkDestroyed(s, "vcp_affinity_group", func(ctx context.Context, id string) error {
		_, err := GetTestClient().GetAffinityGroup(ctx, id)
		return err
	})
}

// CheckSSHKeysDestroyed is a CheckDestroy for vcp_ssh_key resources.
func CheckSSHKeysDestroyed(s *terraform.State) error {
	return checkDestroyed(s, "vcp_ssh_key", func(ctx context.Context, id string) error {
		keyID, err := strconv.Atoi(id)
		if err != nil {
			return fmt.Errorf("unexpected non-numeric SSH key ID %q: %w", id, err)
		}
		_, err = GetTestClient().GetSSHKey(ctx, keyID)
		return err
	})
}

// CheckDNSDomainsDestroyed is a CheckDestroy for vcp_dns_domain resources.
func CheckDNSDomainsDestroyed(s *terraform.State) error {
	return checkDestroyed(s, "vcp_dns_domain", func(ctx context.Context, id string) error {
		_, err := GetTestClient().GetDomain(ctx, id)
		return err
	})
}
