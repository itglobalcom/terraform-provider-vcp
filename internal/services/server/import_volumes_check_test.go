package vstack_server_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestCheckImportedVolumes proves the import check is order-independent: the
// API returns volumes in id order while state after create keeps the config
// order, so the same volumes may arrive in either order. It used to make
// TestAccServer_import a coin flip (ImportStateVerify compares lists
// positionally). Negative cases guard against the check passing vacuously.
func TestCheckImportedVolumes(t *testing.T) {
	expected := []map[string]any{
		{"number": 0, "name": "boot", "size_mb": 30720},
		{"number": 1, "name": "data", "size_mb": 10240},
	}

	state := func(volumes ...[3]string) []*terraform.InstanceState {
		attrs := map[string]string{"volumes.#": strconv.Itoa(len(volumes))}
		for i, v := range volumes {
			p := "volumes." + strconv.Itoa(i)
			attrs[p+".id"] = v[0]
			attrs[p+".name"] = v[1]
			attrs[p+".size_mb"] = v[2]
		}
		return []*terraform.InstanceState{{Attributes: attrs}}
	}

	boot := [3]string{"379", "boot", "30720"}
	data := [3]string{"378", "data", "10240"}

	// Both orders must pass — this is the whole point of the check.
	for _, order := range [][]([3]string){{boot, data}, {data, boot}} {
		if err := checkImportedVolumes(expected)(state(order...)); err != nil {
			t.Errorf("expected order-independent pass, got: %s", err)
		}
	}

	negative := []struct {
		name  string
		state []*terraform.InstanceState
		want  string
	}{
		{"missing volume", state(boot), "expected 2"},
		{"wrong size", state(boot, [3]string{"378", "data", "20480"}), "missing"},
		{"wrong name", state(boot, [3]string{"378", "cache", "10240"}), "missing"},
		{"volume without id", state(boot, [3]string{"0", "data", "10240"}), "no id"},
		{"no volume count", []*terraform.InstanceState{{Attributes: map[string]string{}}}, "volume count"},
		{"two states", append(state(boot, data), state(boot, data)...), "exactly 1"},
	}
	for _, c := range negative {
		err := checkImportedVolumes(expected)(c.state)
		if err == nil {
			t.Errorf("%s: expected an error, got nil", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}
}
