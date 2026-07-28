package ssh_key_test

import (
	"context"
	"fmt"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
)

const (
	testPublicKeyOriginal = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMGzAeuTRIIw1j98rmCsakvwX2FdYq5K8Wg/Jzkgpzj5 example@example.com"
	testPublicKeyUpdated  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMGzAeuTRIIw1j98rmCsakvwX2FdYq5K8Wg/Jzkgpzj5 updated.example@example.com"
)

// TestAccSSHKeyResource_basic covers the key lifecycle: create with checked
// attributes, import round-trip, replace on public_key change (RequiresReplace),
// and an empty plan afterwards.
func TestAccSSHKeyResource_basic(t *testing.T) {
	resourceName := "vcp_ssh_key.test"
	keyName := "tf-acc-ssh-key-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckSSHKeysDestroyed,
		Steps: []resource.TestStep{
			// Step 1: Create the resource
			{
				Config: testAccSSHKeyResourceConfig(keyName, testPublicKeyOriginal),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(keyName),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("public_key"),
						knownvalue.StringExact(testPublicKeyOriginal),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
				},
			},
			// Step 2: Import test
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Step 3: Change public_key only (name unchanged) — the key must be
			// replaced: public_key is RequiresReplace in the schema.
			{
				Config: testAccSSHKeyResourceConfig(keyName, testPublicKeyUpdated),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(
							resourceName,
							plancheck.ResourceActionReplace,
						),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(keyName),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("public_key"),
						knownvalue.StringExact(testPublicKeyUpdated),
					),
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
				},
			},
			// Step 4: Verify idempotency
			{
				Config: testAccSSHKeyResourceConfig(keyName, testPublicKeyUpdated),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

// TestAccSSHKeyResource_disappears deletes the key out-of-band and checks the
// provider detects it is gone (Read → RemoveResource) and plans to recreate it.
// TestAccSSHKeyResource_trailingNewline reproduces the ubiquitous
// `public_key = file("~/.ssh/id_ed25519.pub")` pattern: the file carries a
// trailing newline while the backend stores the key trimmed (verified live).
// Semantic equality on public_key must keep the user's literal value — no
// "inconsistent result" on create and no diff on refresh.
func TestAccSSHKeyResource_trailingNewline(t *testing.T) {
	resourceName := "vcp_ssh_key.test"
	keyName := "tf-acc-ssh-key-nl-" + acctest.RandomString(6)
	keyWithNewline := testPublicKeyOriginal + "\n"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckSSHKeysDestroyed,
		Steps: []resource.TestStep{
			// Step 1: Create — state must keep the config literal (with the newline).
			{
				Config: testAccSSHKeyResourceConfig(keyName, keyWithNewline),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						resourceName,
						tfjsonpath.New("public_key"),
						knownvalue.StringExact(keyWithNewline),
					),
				},
			},
			// Step 2: Re-plan the same config — the API returns the trimmed key,
			// but the plan must stay empty.
			{
				Config: testAccSSHKeyResourceConfig(keyName, keyWithNewline),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})
}

func TestAccSSHKeyResource_disappears(t *testing.T) {
	keyName := "tf-acc-ssh-key-dis-" + acctest.RandomString(6)
	config := testAccSSHKeyResourceConfig(keyName, testPublicKeyOriginal)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckSSHKeysDestroyed,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { deleteSSHKeyOutOfBand(t, keyName) },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// deleteSSHKeyOutOfBand removes the key with the given name directly via the API.
func deleteSSHKeyOutOfBand(t *testing.T, name string) {
	client := acctest.GetTestClient()
	ctx := context.Background()

	keys, err := client.GetSSHKeyList(ctx)
	if err != nil {
		t.Fatalf("out-of-band SSH key list failed: %v", err)
	}
	for _, key := range keys {
		if key.Name == name {
			if err := client.DeleteSSHKey(ctx, key.ID); err != nil {
				t.Fatalf("out-of-band SSH key delete failed: %v", err)
			}
			return
		}
	}
	t.Fatalf("SSH key %s not found to delete out-of-band", name)
}

func testAccSSHKeyResourceConfig(name, publicKey string) string {
	return fmt.Sprintf(`
resource "vcp_ssh_key" "test" {
  name       = %q
  public_key = %q
}
`, name, publicKey)
}

// --------------------
// Data source: ssh_key
// --------------------

// TestAccSSHKeyDataSource_byID verifies the singular data source returns the
// same id/name/public_key as the resource it looks up.
func TestAccSSHKeyDataSource_byID(t *testing.T) {
	resourceName := "vcp_ssh_key.test"
	dataSourceName := "data.vcp_ssh_key.test"
	keyName := "tf-acc-ssh-key-ds-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckSSHKeysDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccSSHKeyDataSourceConfig(keyName, testPublicKeyOriginal),
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the data source ID matches the resource ID
					statecheck.CompareValuePairs(
						dataSourceName,
						tfjsonpath.New("id"),
						resourceName,
						tfjsonpath.New("id"),
						compare.ValuesSame(),
					),
					// Verify that the data source name matches the resource name
					statecheck.CompareValuePairs(
						dataSourceName,
						tfjsonpath.New("name"),
						resourceName,
						tfjsonpath.New("name"),
						compare.ValuesSame(),
					),
					// Verify that the data source public_key matches the resource public_key
					statecheck.CompareValuePairs(
						dataSourceName,
						tfjsonpath.New("public_key"),
						resourceName,
						tfjsonpath.New("public_key"),
						compare.ValuesSame(),
					),

					// Additionally: verify specific values
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("name"),
						knownvalue.StringExact(keyName),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("public_key"),
						knownvalue.StringExact(testPublicKeyOriginal),
					),
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("id"),
						knownvalue.NotNull(),
					),
				},
			},
		},
	})
}

func testAccSSHKeyDataSourceConfig(name, publicKey string) string {
	return fmt.Sprintf(`
resource "vcp_ssh_key" "test" {
  name       = %q
  public_key = %q
}

data "vcp_ssh_key" "test" {
  id = vcp_ssh_key.test.id
}
`, name, publicKey)
}

// ----------------------
// Data source: ssh_keys
// ----------------------

// TestAccSSHKeysDataSource_list verifies the plural data source includes a
// freshly created key with the right fields (list size is not asserted — the
// account may hold other keys).
func TestAccSSHKeysDataSource_list(t *testing.T) {
	dataSourceName := "data.vcp_ssh_keys.test"
	keyName := "tf-acc-ssh-key-list-" + acctest.RandomString(6)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             acctest.CheckSSHKeysDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccSSHKeysDataSourceConfig(keyName, testPublicKeyOriginal),
				ConfigStateChecks: []statecheck.StateCheck{
					// Verify that the data source returned at least one SSH key
					statecheck.ExpectKnownValue(
						dataSourceName,
						tfjsonpath.New("ssh_keys"),
						knownvalue.NotNull(),
					),

					// The account may contain keys beyond those created by this
					// test, so don't assert the list size or element positions —
					// only that the created key is present with the right fields.
					checkSSHKeyInList{
						dataSourceAddress: dataSourceName,
						expectedName:      keyName,
						expectedPublicKey: testPublicKeyOriginal,
					},
				},
			},
		},
	})
}

// checkSSHKeyInList - custom state check to verify an SSH key with a specific
// name exists in the list with the expected public key and a non-empty id.
type checkSSHKeyInList struct {
	dataSourceAddress string
	expectedName      string
	expectedPublicKey string
}

func (c checkSSHKeyInList) CheckState(
	ctx context.Context,
	req statecheck.CheckStateRequest,
	resp *statecheck.CheckStateResponse,
) {
	var resource *tfjson.StateResource
	for _, r := range req.State.Values.RootModule.Resources {
		if r.Address == c.dataSourceAddress {
			resource = r
			break
		}
	}

	if resource == nil {
		resp.Error = fmt.Errorf("Resource not found: %s", c.dataSourceAddress)
		return
	}

	keysRaw, ok := resource.AttributeValues["ssh_keys"]
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'ssh_keys' not found")
		return
	}

	keys, ok := keysRaw.([]any)
	if !ok {
		resp.Error = fmt.Errorf("Attribute 'ssh_keys' is not a list")
		return
	}

	for _, keyRaw := range keys {
		key, ok := keyRaw.(map[string]any)
		if !ok {
			continue
		}

		if name, _ := key["name"].(string); name != c.expectedName {
			continue
		}

		if publicKey, _ := key["public_key"].(string); publicKey != c.expectedPublicKey {
			resp.Error = fmt.Errorf(
				"SSH key %s found but public_key mismatch: expected %s, got %v",
				c.expectedName,
				c.expectedPublicKey,
				key["public_key"],
			)
			return
		}

		if key["id"] == nil {
			resp.Error = fmt.Errorf("SSH key %s found but id is empty", c.expectedName)
			return
		}

		return
	}

	resp.Error = fmt.Errorf("SSH key with name %s not found in list", c.expectedName)
}

func testAccSSHKeysDataSourceConfig(name, publicKey string) string {
	return fmt.Sprintf(`
resource "vcp_ssh_key" "test_list" {
  name       = %q
  public_key = %q
}

data "vcp_ssh_keys" "test" {
  depends_on = [vcp_ssh_key.test_list]
}
`, name, publicKey)
}
