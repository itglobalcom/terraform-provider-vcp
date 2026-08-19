// Package locks serializes mutating API operations per parent object.
//
// The API serializes changes to a server/gateway on its side: concurrent NIC
// operations on the same parent answer -19605 "Server is busy" / -19803
// "competitive change conflict" and burn through SDK retries (and can trip
// long-lasting Busy states on the backend). Terraform runs independent
// resources in parallel (default parallelism=10), so several attachments of
// one server would race each other. Locking per parent ID keeps operations on
// ONE server/gateway sequential while different parents still run in parallel.
//
// Same approach as the AWS provider's global MutexKV.
package locks

import (
	"strconv"
	"sync"
)

var (
	mu    sync.Mutex
	byKey = map[string]*sync.Mutex{}
)

// lock acquires the mutex for key and returns its unlock function:
//
//	defer locks.Server(id)()
func lock(key string) func() {
	mu.Lock()
	l, ok := byKey[key]
	if !ok {
		l = &sync.Mutex{}
		byKey[key] = l
	}
	mu.Unlock()

	l.Lock()
	return l.Unlock
}

// Server serializes mutating operations on a single server across all
// resource types (vcp_server, vcp_server_public_interface,
// vcp_server_network_attachment).
func Server(id string) func() { return lock("server:" + id) }

// Gateway serializes mutating operations on a single gateway across all
// resource types (vcp_gateway, vcp_gateway_network_attachment,
// vcp_gateway_nat, vcp_gateway_firewall).
func Gateway(id string) func() { return lock("gateway:" + id) }

// VmwareNetwork serializes mutating operations on a single VMware network and on
// the edge that belongs to it (vcp_vmware_network, vcp_vmware_edge_firewall,
// vcp_vmware_edge_nat). The platform re-pushes the whole set of edge objects to
// vCloud even when a single rule changes, so two concurrent edge writes on one
// network answer 409 -4000 and can leave the edge half-applied.
func VmwareNetwork(id int) func() { return lock("vmware-network:" + strconv.Itoa(id)) }

// VmwareServer serializes mutating operations on a single VMware server
// (vcp_vmware_server, vcp_vmware_server_firewall,
// vcp_vmware_server_network_attachment, vcp_vmware_server_public_interface).
func VmwareServer(id int) func() { return lock("vmware-server:" + strconv.Itoa(id)) }
