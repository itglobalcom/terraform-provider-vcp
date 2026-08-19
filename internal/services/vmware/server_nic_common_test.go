package vmware

import (
	"testing"

	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

func nic(id, networkID int, primary bool, ip string) *entities.VmwareNIC {
	n := &entities.VmwareNIC{ID: id, NetworkID: networkID, IsPrimary: primary}
	if ip != "" {
		n.IP = strPtr(ip)
	}
	return n
}

func TestFindVmwareNIC(t *testing.T) {
	nics := []*entities.VmwareNIC{nic(1, 100, true, "203.0.113.5"), nil, nic(2, 200, false, "10.0.0.5")}

	if got := findVmwareNIC(nics, 2); got == nil || got.ID != 2 {
		t.Errorf("findVmwareNIC(2) = %v, want the interface with id 2", got)
	}
	if got := findVmwareNIC(nics, 99); got != nil {
		t.Errorf("findVmwareNIC(99) = %v, want nil", got)
	}
	if got := findVmwareNIC(nil, 1); got != nil {
		t.Errorf("findVmwareNIC on an empty list = %v, want nil", got)
	}
}

// Attaching a server to a network it is already on is legitimate, so the new
// interface can only be told apart by not having been in the snapshot — matching
// on the network alone would return the older one and leak the new one.
func TestFindNewVmwareNICIgnoresInterfacesFromTheSnapshot(t *testing.T) {
	before := []*entities.VmwareNIC{nic(1, 100, true, ""), nic(2, 200, false, "10.0.0.5")}
	after := append(append([]*entities.VmwareNIC{}, before...), nic(3, 200, false, "10.0.0.6"))

	got := findNewVmwareNIC(before, after, func(n *entities.VmwareNIC) bool { return n.NetworkID == 200 })
	if got == nil || got.ID != 3 {
		t.Fatalf("findNewVmwareNIC = %v, want the newly added interface (id 3)", got)
	}
}

func TestFindNewVmwareNICHonoursTheMatcher(t *testing.T) {
	before := []*entities.VmwareNIC{nic(1, 100, true, "")}
	after := []*entities.VmwareNIC{nic(1, 100, true, ""), nic(2, 200, false, "10.0.0.5")}

	// A matcher no new interface satisfies must yield nothing rather than the
	// nearest interface — the caller reports it instead of recording the wrong one.
	if got := findNewVmwareNIC(before, after, func(n *entities.VmwareNIC) bool { return n.NetworkID == 999 }); got != nil {
		t.Errorf("findNewVmwareNIC with a matcher nothing satisfies = %v, want nil", got)
	}

	// The primary interface belongs to the server resource, so a public-interface
	// matcher excludes it even when the snapshot could not be taken.
	got := findNewVmwareNIC(nil, after, func(n *entities.VmwareNIC) bool { return !n.IsPrimary })
	if got == nil || got.ID != 2 {
		t.Fatalf("findNewVmwareNIC = %v, want the non-primary interface (id 2)", got)
	}
}

// A nil entry in either list must not panic: the API's NIC list is decoded as a
// slice of pointers.
func TestFindNewVmwareNICToleratesNilEntries(t *testing.T) {
	before := []*entities.VmwareNIC{nil, nic(1, 100, true, "")}
	after := []*entities.VmwareNIC{nil, nic(1, 100, true, ""), nic(2, 200, false, "")}

	got := findNewVmwareNIC(before, after, func(n *entities.VmwareNIC) bool { return true })
	if got == nil || got.ID != 2 {
		t.Fatalf("findNewVmwareNIC = %v, want the interface with id 2", got)
	}
}

func TestParseServerNICImportID(t *testing.T) {
	serverID, nicID, ok := parseServerNICImportID("5678:9012")
	if !ok || serverID != 5678 || nicID != 9012 {
		t.Fatalf(`parseServerNICImportID("5678:9012") = (%d, %d, %v), want (5678, 9012, true)`, serverID, nicID, ok)
	}

	invalid := []string{
		"",
		"5678",          // no separator
		"5678:",         // no nic
		":9012",         // no server
		"5678:0",        // ids are positive
		"0:9012",        // ids are positive
		"5678:-1",       // ids are positive
		"5678:abc",      // not a number
		"5678/9012",     // wrong separator
		"5678:9012:foo", // a third field means the caller meant something else
	}
	for _, id := range invalid {
		if _, _, ok := parseServerNICImportID(id); ok {
			t.Errorf("parseServerNICImportID(%q) accepted an invalid import id", id)
		}
	}
}

func TestNicIP(t *testing.T) {
	if got := nicIP(nic(1, 100, false, "10.0.0.5")); got != "10.0.0.5" {
		t.Errorf("nicIP = %q, want 10.0.0.5", got)
	}
	if got := nicIP(nic(1, 100, false, "")); got != "" {
		t.Errorf("nicIP of an interface without an address = %q, want an empty string", got)
	}
}
