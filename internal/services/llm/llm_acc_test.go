package llm_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/itglobalcom/terraform-provider-vcp/internal/acctest"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// ============================================================================
// vcp_llm_connection
// ============================================================================

// TestAccLlmConnection_existing takes the connection of an already connected
// project into the state. The names in the configuration are not those of the
// project: a provider that connected again would either fail on
// ServiceAlreadyConnected or leave a group with that name behind.
func TestAccLlmConnection_existing(t *testing.T) {
	resourceName := "vcp_llm_connection.test"
	groupName := testName("conn-group")
	config := fmt.Sprintf(`
resource "vcp_llm_connection" "test" {
  group_name     = %q
  first_key_name = %q
}
`, groupName, testName("conn-key"))

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckLLM(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             checkLLMDestroyed,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("id"), knownvalue.StringExact(llmProjectID())),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("state"), knownvalue.StringExact("connected")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("group_name"), knownvalue.StringExact(groupName)),
				},
				Check: checkNoGroupNamed(groupName),
			},
			{Config: config, PlanOnly: true},
			// The id is the project's; the names are inputs of a connection made
			// in the panel and need not match the configuration.
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateId:           llmProjectID(),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"group_name", "first_key_name"},
			},
		},
	})
}

// TestAccLlmConnection_importForeignProject refuses an id that is not the
// project of the provider key: there is no other connection it could mean.
func TestAccLlmConnection_importForeignProject(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckLLM(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "vcp_llm_connection" "test" {
  group_name     = "test-acc-llm-unused"
  first_key_name = "test-acc-llm-unused"
}
`,
				ResourceName:  "vcp_llm_connection.test",
				ImportState:   true,
				ImportStateId: "1" + llmProjectID(),
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func checkNoGroupNamed(name string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		groups, err := acctest.GetTestClient().GetLLMGroupList(context.Background())
		if err != nil {
			return fmt.Errorf("listing LLM groups: %w", err)
		}
		for _, g := range groups {
			if g.Name == name {
				return fmt.Errorf("the project has a group %q: the connection was made again", name)
			}
		}
		return nil
	}
}

// ============================================================================
// vcp_llm_group
// ============================================================================

// TestAccLlmGroup_lifecycle is the life of a group: create, empty plan, models
// and tags edited in place, drift made in the panel, a rename that replaces the
// group, import, and a destroy that disables it.
func TestAccLlmGroup_lifecycle(t *testing.T) {
	resourceName := "vcp_llm_group.test"
	name := testName("group")
	var groupID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckLLM(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             checkLLMDestroyed,
		Steps: []resource.TestStep{
			{
				Config: modelsConfig + groupConfig(name, 1, []string{"a"}),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("state"), knownvalue.StringExact("active")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("model_ids"), knownvalue.SetSizeExact(1)),
				},
				Check: resource.ComposeTestCheckFunc(
					checkGroupInAPI(resourceName, 1, []string{"a"}, entities.LLMGroupStateActive),
					captureAttr(resourceName, "id", &groupID),
				),
			},
			{Config: modelsConfig + groupConfig(name, 1, []string{"a"}), PlanOnly: true},
			// Models and tags change in place, not by a new group.
			{
				Config: modelsConfig + groupConfig(name, 2, []string{"a", "b"}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: checkGroupInAPI(resourceName, 2, []string{"a", "b"}, entities.LLMGroupStateActive),
			},
			// Tags changed in the panel show up as drift.
			{
				PreConfig: func() {
					if err := acctest.GetTestClient().SetLLMGroupTags(context.Background(),
						entities.LLMGroupID(mustAtoi(t, groupID)), &entities.SetLLMTagsRequest{Tags: []string{"panel"}}); err != nil {
						t.Fatalf("out-of-band tag change failed: %v", err)
					}
				},
				Config:             modelsConfig + groupConfig(name, 2, []string{"a", "b"}),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: modelsConfig + groupConfig(name, 2, []string{"a", "b"}),
				Check:  checkGroupInAPI(resourceName, 2, []string{"a", "b"}, entities.LLMGroupStateActive),
			},
			// A new name is a new group: the old one is disabled.
			{
				Config: modelsConfig + groupConfig(name+"-2", 2, []string{"a", "b"}),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					checkGroupInAPI(resourceName, 2, []string{"a", "b"}, entities.LLMGroupStateActive),
					checkGroupDisabled(&groupID),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func checkGroupDisabled(id *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		group, err := acctest.GetTestClient().GetLLMGroup(context.Background(), entities.LLMGroupID(mustAtoiErr(*id)))
		if err != nil {
			return fmt.Errorf("reading the replaced LLM group %s: %w", *id, err)
		}
		if group.State == nil || *group.State != entities.LLMGroupStateDisabled {
			return fmt.Errorf("the replaced LLM group %s is %v, want disabled", *id, group.State)
		}
		return nil
	}
}

// ============================================================================
// vcp_llm_access_key
// ============================================================================

func keyConfig(groupName, keyName string, tags []string, lock string) string {
	return modelsConfig + groupConfig(groupName, 1, nil) + fmt.Sprintf(`
resource "vcp_llm_access_key" "test" {
  group_id = vcp_llm_group.test.id
  name     = %q
  tags     = %s
%s}
`, keyName, hclStrings(tags), lock)
}

// TestAccLlmAccessKey_lifecycle: the secret is taken once at the issue and
// kept, tags and the client lock change in place, an import comes without the
// secret, and a new name is a new key with a new secret.
func TestAccLlmAccessKey_lifecycle(t *testing.T) {
	resourceName := "vcp_llm_access_key.test"
	groupName, keyName := testName("kgroup"), testName("key")
	var firstSecret string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckLLM(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             checkLLMDestroyed,
		Steps: []resource.TestStep{
			{
				Config: keyConfig(groupName, keyName, []string{"a"}, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectSensitiveValue(resourceName, tfjsonpath.New("secret")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("secret"),
						knownvalue.StringRegexp(regexp.MustCompile(`.+`))),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("state"), knownvalue.StringExact("active")),
					statecheck.ExpectKnownValue(resourceName, tfjsonpath.New("locked"), knownvalue.Bool(false)),
				},
				Check: resource.ComposeTestCheckFunc(
					checkKeyLockInAPI(resourceName, false, ""),
					captureAttr(resourceName, "secret", &firstSecret),
				),
			},
			{Config: keyConfig(groupName, keyName, []string{"a"}, ""), PlanOnly: true},
			{
				Config: keyConfig(groupName, keyName, []string{"a", "b"}, `  locked      = true
  lock_reason = "test-acc"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					checkKeyLockInAPI(resourceName, true, "test-acc"),
					checkKeyTagsInAPI(resourceName, 2),
					resource.TestCheckResourceAttrPtr(resourceName, "secret", &firstSecret),
				),
			},
			// A new reason on a locked key: the API refuses a second lock, so the
			// provider unlocks and locks again.
			{
				Config: keyConfig(groupName, keyName, []string{"a", "b"}, `  locked      = true
  lock_reason = "test-acc-rotation"
`),
				Check: checkKeyLockInAPI(resourceName, true, "test-acc-rotation"),
			},
			{
				Config: keyConfig(groupName, keyName, []string{"a", "b"}, ""),
				Check:  checkKeyLockInAPI(resourceName, false, ""),
			},
			// The API does not hand the secret out again.
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateIdFunc:       keyImportID(resourceName),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("imported %d instances, want 1", len(states))
					}
					if secret := states[0].Attributes["secret"]; secret != "" {
						return fmt.Errorf("an imported key has a secret")
					}
					return nil
				},
			},
			{
				Config: keyConfig(groupName, keyName+"-2", []string{"a", "b"}, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: func(s *terraform.State) error {
					secret, err := attr(s, resourceName, "secret")
					if err != nil {
						return err
					}
					if secret == firstSecret {
						return fmt.Errorf("the replacing key kept the secret of the replaced one")
					}
					return nil
				},
			},
		},
	})
}

func checkKeyTagsInAPI(address string, want int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		key, err := keyFromAPI(s, address)
		if err != nil {
			return err
		}
		if len(key.Tags) != want {
			return fmt.Errorf("access key %s has tags %v, want %d", address, key.Tags, want)
		}
		return nil
	}
}

// TestAccLlmAccessKey_severalInOneGroup applies several keys of one group at
// once, and changes them all at once: the platform serializes changes to a
// group, so without the group lock the parallel applies would race.
func TestAccLlmAccessKey_severalInOneGroup(t *testing.T) {
	groupName := testName("multi")
	keyName := testName("key")
	config := func(tags []string, locked bool) string {
		return modelsConfig + groupConfig(groupName, 1, nil) + fmt.Sprintf(`
resource "vcp_llm_access_key" "test" {
  count    = 3
  group_id = vcp_llm_group.test.id
  name     = "%s-${count.index}"
  tags     = %s
  locked   = %t
}
`, keyName, hclStrings(tags), locked)
	}

	checks := func(locked bool) resource.TestCheckFunc {
		var fns []resource.TestCheckFunc
		for i := 0; i < 3; i++ {
			address := fmt.Sprintf("vcp_llm_access_key.test.%d", i)
			fns = append(fns,
				resource.TestCheckResourceAttrSet(address, "secret"),
				checkKeyLockInAPI(address, locked, ""))
		}
		fns = append(fns, checkActiveKeysInGroup("vcp_llm_group.test", 3))
		return resource.ComposeTestCheckFunc(fns...)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckLLM(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             checkLLMDestroyed,
		Steps: []resource.TestStep{
			{Config: config(nil, false), Check: checks(false)},
			{Config: config([]string{"x"}, true), Check: checks(true)},
		},
	})
}

func checkActiveKeysInGroup(groupAddress string, want int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id, err := intAttr(s, groupAddress, "id")
		if err != nil {
			return err
		}
		keys, err := acctest.GetTestClient().GetLLMAccessKeyList(context.Background(), entities.LLMGroupID(id))
		if err != nil {
			return fmt.Errorf("listing access keys of LLM group %d: %w", id, err)
		}
		active := 0
		for _, k := range keys {
			if k.State != nil && *k.State == entities.LLMAccessKeyStateActive {
				active++
			}
		}
		if active != want {
			return fmt.Errorf("LLM group %d has %d active keys, want %d", id, active, want)
		}
		return nil
	}
}

// ============================================================================
// Data sources
// ============================================================================

// TestAccLlmDataSources reads the catalog, one model of it, the package and the
// plans the way the documentation tells a user to.
func TestAccLlmDataSources(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckLLM(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: modelsConfig + `
data "vcp_llm_model" "first" {
  id = local.model_ids[0]
}

data "vcp_llm_package" "current" {}

data "vcp_llm_package_plans" "all" {}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					acctest.CheckListNotEmpty("data.vcp_llm_models.all", "models"),
					statecheck.ExpectKnownValue("data.vcp_llm_model.first", tfjsonpath.New("is_available_to_groups"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("data.vcp_llm_model.first", tfjsonpath.New("endpoint"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.vcp_llm_model.first", tfjsonpath.New("default_provider"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.vcp_llm_package.current", tfjsonpath.New("currency"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.vcp_llm_package.current", tfjsonpath.New("usage"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("data.vcp_llm_package_plans.all", tfjsonpath.New("plans"), knownvalue.NotNull()),
				},
			},
		},
	})
}
