# VMware: конфигурации приёмочных тестов

Сгенерировано из `internal/services/vmware/config_syntax_test.go`. Не редактировать руками —
перегенерировать:

```sh
VMWARE_CONFIG_DUMP=$PWD/vmware_acceptance_configs.md go test ./internal/services/vmware -run TestDumpAcceptanceConfigs
```

Это ровно тот HCL, который приёмочные тесты применяют к живому API, с двумя
подстановками вместо переменных окружения: `location_id = 5` (`VCP_VMWARE_LOCATION_ID`)
и `image_id = 42` (`VCP_VMWARE_IMAGE_ID`). Имена ресурсов в реальном прогоне
получают случайный суффикс — `test-acc-vmw-<вид>-<6 символов>` — чтобы два прогона в
одном проекте не сталкивались, а `make sweep` мог убрать за упавшим.

Размеры машин нигде не захардкожены: они выводятся из каталога через
`vcp_vmware_locations` и `vcp_vmware_images`, как это делал бы пользователь. Поэтому
набор идёт на любом стенде, а заодно каждый серверный тест проверяет справочники.

Каждая конфигурация ниже — самостоятельный `.tf`: её можно скопировать целиком,
подставить свои `location_id`/`image_id` и применить.

## Оглавление

- [`fragment/catalog`](#fragmentcatalog) — Reads the catalog and derives the sizing every server configuration below uses. A location that offered no system disk type would fail here, at plan time.
- [`fragment/server`](#fragmentserver) — The catalog fragment plus one server sized from it.
- [`fragment/routedNetwork`](#fragmentroutednetwork) — The network the edge tests need — only a routed network carries an edge.
- [`network/isolated`](#networkisolated) — TestAccVmwareNetwork_isolated, _forcesReplacement, _disappears.
- [`network/withDataSource`](#networkwithdatasource) — TestAccVmwareNetwork_isolated — the same network read back through the by-id data source.
- [`network/routedBandwidth`](#networkroutedbandwidth) — TestAccVmwareNetwork_routed — the bandwidth raised in place (NET-8: this is also the edge's bandwidth).
- [`network/public`](#networkpublic) — TestAccVmwareNetwork_public — ordered by capacity rather than by address range.
- [`server/basic`](#serverbasic) — TestAccVmwareServer_basic, _computerNameIsCaseInsensitive, _updateInPlace, _disappears.
- [`server/resized`](#serverresized) — TestAccVmwareServer_updateInPlace — twice the CPU and RAM, one disk step bigger, renamed.
- [`server/otherImage`](#serverotherimage) — TestAccVmwareServer_imageForcesReplacement — a different image from the catalog.
- [`server/dataSources`](#serverdatasources) — TestAccVmwareServer_dataSources — every VMware data source at once.
- [`server/byName`](#serverbyname) — TestAccVmwareServer_dataSourceByName — looking a machine up by the name on the screen.
- [`server/bandwidth`](#serverbandwidth) — TestAccVmwareServer_bandwidthInPlace — the bandwidth of the interface the machine is born with.
- [`volumes/one`](#volumesone) — TestAccVmwareServerVolumes_lifecycle — one data disk beside the boot disk.
- [`volumes/grown`](#volumesgrown) — TestAccVmwareServerVolumes_lifecycle — the same disk renamed and grown by one step of its type.
- [`volumes/two`](#volumestwo) — TestAccVmwareServerVolumes_lifecycle — a second disk alongside the first.
- [`copy/basic`](#copybasic) — TestAccVmwareServerCopy_lifecycle — a machine and a copy of it, which states only where it comes from and what it is called.
- [`copy/resized`](#copyresized) — TestAccVmwareServerCopy_lifecycle — the same copy resized in place, which is what a copy is once it exists.
- [`copy/withImage`](#copywithimage) — TestAccVmwareServerCopy_refusesOrderArguments — a copy and an image at once, refused at plan time.
- [`snapshot/one`](#snapshotone) — TestAccVmwareServerSnapshot_lifecycle, _disappears — the single snapshot a VMware server can hold.
- [`snapshot/two`](#snapshottwo) — TestAccVmwareServerSnapshot_secondIsRefused — a second snapshot of one machine, which the platform allows no server to hold.
- [`nic/attachmentBase`](#nicattachmentbase) — TestAccVmwareServerNetworkAttachment_basic (last step) — server and network with no attachment between them.
- [`nic/attachmentStatic`](#nicattachmentstatic) — TestAccVmwareServerNetworkAttachment_basic, _ipForcesReplacement, _disappears.
- [`nic/attachmentNoIP`](#nicattachmentnoip) — The same attachment with the address left to the platform.
- [`nic/attachmentDHCP`](#nicattachmentdhcp) — TestAccVmwareServerNetworkAttachment_dhcp — a network that hands out addresses.
- [`nic/publicInterface`](#nicpublicinterface) — TestAccVmwareServerPublicInterface_basic — a second public address.
- [`nic/parallel`](#nicparallel) — TestAccVmwareServerNICs_parallel — a private attachment and a public interface on one machine in one apply (locks.VmwareServer).
- [`rules/edgeFirewallAllFields`](#rulesedgefirewallallfields) — TestAccVmwareEdgeFirewall_basic — every attribute a rule has.
- [`rules/edgeFirewallShort`](#rulesedgefirewallshort) — TestAccVmwareEdgeFirewall_drift, _adoptsExistingRules.
- [`rules/edgeFirewallEmpty`](#rulesedgefirewallempty) — TestAccVmwareEdgeFirewall_basic — an empty list: the firewall stays on and default_action becomes its whole behaviour.
- [`rules/edgeNATPair`](#rulesedgenatpair) — TestAccVmwareEdgeNAT_basic — one rule of each kind. original_ip is absent on purpose (NET-4).
- [`rules/edgeNATSingle`](#rulesedgenatsingle) — TestAccVmwareEdgeNAT_growAndShrink, _drift — the starting point.
- [`rules/edgeNATThree`](#rulesedgenatthree) — TestAccVmwareEdgeNAT_growAndShrink — grown to three: the first rule is overwritten in place, two are created.
- [`rules/edgeNATEmpty`](#rulesedgenatempty) — An empty NAT list — every rule removed from the edge.
- [`rules/edgeParallel`](#rulesedgeparallel) — TestAccVmwareEdgeRules_parallel — firewall and NAT of one network in one apply (locks.VmwareNetwork).
- [`rules/serverFirewallFull`](#rulesserverfirewallfull) — TestAccVmwareServerFirewall_basic — both directions, a closing deny rule.
- [`rules/serverFirewallOne`](#rulesserverfirewallone) — TestAccVmwareServerFirewall_basic (update), _drift.
- [`rules/serverFirewallNone`](#rulesserverfirewallnone) — An empty server firewall — every rule removed, the machine left unfiltered.
- [`vpn/tunnel`](#vpntunnel) — TestAccVmwareEdgeVPNTunnel_basic — a site-to-site tunnel on the edge of a routed network.
- [`vpn/tunnelDisabled`](#vpntunneldisabled) — TestAccVmwareEdgeVPNTunnel_basic — the same tunnel with a smaller MTU, switched off.

---

## fragment/catalog

Reads the catalog and derives the sizing every server configuration below uses. A location that offered no system disk type would fail here, at plan time.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}
```

## fragment/server

The catalog fragment plus one server sized from it.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
  computer_name    = "host01"
}
```

## fragment/routedNetwork

The network the edge tests need — only a routed network carries an edge.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.240.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}
```

## network/isolated

TestAccVmwareNetwork_isolated, _forcesReplacement, _disappears.

```hcl
resource "vcp_vmware_network" "test" {
  type        = "isolated"
  location_id = 5
  name        = "test-acc-vmw-syntax"
  address     = "10.240.1.0"
  mask        = 24
  enable_dhcp = false
}
```

## network/withDataSource

TestAccVmwareNetwork_isolated — the same network read back through the by-id data source.

```hcl
resource "vcp_vmware_network" "test" {
  type        = "isolated"
  location_id = 5
  name        = "test-acc-vmw-syntax"
  address     = "10.240.1.0"
  mask        = 24
  enable_dhcp = false
}

data "vcp_vmware_network" "by_id" {
  id = vcp_vmware_network.test.id
}
```

## network/routedBandwidth

TestAccVmwareNetwork_routed — the bandwidth raised in place (NET-8: this is also the edge's bandwidth).

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.240.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 30
}
```

## network/public

TestAccVmwareNetwork_public — ordered by capacity rather than by address range.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "public"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  capacity       = "1"
  bandwidth_mbps = 20
}
```

## server/basic

TestAccVmwareServer_basic, _computerNameIsCaseInsensitive, _updateInPlace, _disappears.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
  computer_name    = "host01"
}
```

## server/resized

TestAccVmwareServer_updateInPlace — twice the CPU and RAM, one disk step bigger, renamed.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  computer_name    = "host01"
  image_id         = 42
  cpu              = 2
  ram_mb           = local.ram_mb * 2
  system_disk_mb   = local.system_disk_mb + local.disk_type.step_mb
  system_disk_type = local.disk_type.title
}
```

## server/otherImage

TestAccVmwareServer_imageForcesReplacement — a different image from the catalog.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

locals {
  other_image = [for i in data.vcp_vmware_images.all.images : i if i.id != 42 && !i.is_gpu_only][0]
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  computer_name    = "host01"
  image_id         = local.other_image.id
  cpu              = 1
  ram_mb           = max(1024, local.other_image.min_ram_mb)
  system_disk_mb   = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.other_image.hdd_gb * 1024)
  system_disk_type = local.disk_type.title
}
```

## server/dataSources

TestAccVmwareServer_dataSources — every VMware data source at once.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

data "vcp_vmware_images" "non_gpu" {
  location_id = 5
  gpu         = "unsupported"
}

data "vcp_vmware_gpu_models" "all" {
  location_id = 5
}

data "vcp_vmware_server" "by_id" {
  id = vcp_vmware_server.test.id
}

data "vcp_vmware_servers" "all" {
  location_id = 5
}

data "vcp_vmware_networks" "all" {
  location_id = 5
}
```

## server/byName

TestAccVmwareServer_dataSourceByName — looking a machine up by the name on the screen.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

data "vcp_vmware_server" "by_name" {
  name = vcp_vmware_server.test.name
}
```

## server/bandwidth

TestAccVmwareServer_bandwidthInPlace — the bandwidth of the interface the machine is born with.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id            = 5
  name                   = "test-acc-vmw-syntax"
  image_id               = 42
  cpu                    = 1
  ram_mb                 = local.ram_mb
  system_disk_mb         = local.system_disk_mb
  system_disk_type       = local.disk_type.title
  network_bandwidth_mbps = 10
}
```

## volumes/one

TestAccVmwareServerVolumes_lifecycle — one data disk beside the boot disk.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
  volumes = [
    {
      number    = 0
      name      = "data"
      size_mb   = local.disk_type.min_mb
      disk_type = local.disk_type.title
    },
  ]
}
```

## volumes/grown

TestAccVmwareServerVolumes_lifecycle — the same disk renamed and grown by one step of its type.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
  volumes = [
    {
      number    = 0
      name      = "db-data"
      size_mb   = local.disk_type.min_mb + local.disk_type.step_mb
      disk_type = local.disk_type.title
    },
  ]
}
```

## volumes/two

TestAccVmwareServerVolumes_lifecycle — a second disk alongside the first.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
  volumes = [
    {
      number    = 0
      name      = "db-data"
      size_mb   = local.disk_type.min_mb + local.disk_type.step_mb
      disk_type = local.disk_type.title
    },
    {
      number    = 1
      name      = "logs"
      size_mb   = local.disk_type.min_mb
      disk_type = local.disk_type.title
    },
  ]
}
```

## copy/basic

TestAccVmwareServerCopy_lifecycle — a machine and a copy of it, which states only where it comes from and what it is called.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server" "copy" {
  copy_from_server_id = vcp_vmware_server.test.id
  name                = "test-acc-vmw-syntax-copy"
}
```

## copy/resized

TestAccVmwareServerCopy_lifecycle — the same copy resized in place, which is what a copy is once it exists.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server" "copy" {
  copy_from_server_id = vcp_vmware_server.test.id
  name                = "test-acc-vmw-syntax-copy"
  cpu                 = 2
}
```

## copy/withImage

TestAccVmwareServerCopy_refusesOrderArguments — a copy and an image at once, refused at plan time.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server" "copy" {
  copy_from_server_id = vcp_vmware_server.test.id
  name                = "test-acc-vmw-syntax-copy"
  image_id            = 42
}
```

## snapshot/one

TestAccVmwareServerSnapshot_lifecycle, _disappears — the single snapshot a VMware server can hold.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_snapshot" "test" {
  server_id = vcp_vmware_server.test.id
  name      = "before-upgrade"
}
```

## snapshot/two

TestAccVmwareServerSnapshot_secondIsRefused — a second snapshot of one machine, which the platform allows no server to hold.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_snapshot" "test" {
  server_id = vcp_vmware_server.test.id
  name      = "first"
}

resource "vcp_vmware_server_snapshot" "second" {
  server_id = vcp_vmware_server.test.id
  name      = "second"
}
```

## nic/attachmentBase

TestAccVmwareServerNetworkAttachment_basic (last step) — server and network with no attachment between them.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_network" "test" {
  type        = "isolated"
  location_id = 5
  name        = "test-acc-vmw-syntax-net"
  address     = "10.240.1.0"
  mask        = 24
  enable_dhcp = false
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}
```

## nic/attachmentStatic

TestAccVmwareServerNetworkAttachment_basic, _ipForcesReplacement, _disappears.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_network" "test" {
  type        = "isolated"
  location_id = 5
  name        = "test-acc-vmw-syntax-net"
  address     = "10.240.1.0"
  mask        = 24
  enable_dhcp = false
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_network_attachment" "test" {
  server_id  = vcp_vmware_server.test.id
  network_id = vcp_vmware_network.test.id
  ip         = "10.240.1.10"
}
```

## nic/attachmentNoIP

The same attachment with the address left to the platform.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_network" "test" {
  type        = "isolated"
  location_id = 5
  name        = "test-acc-vmw-syntax-net"
  address     = "10.240.1.0"
  mask        = 24
  enable_dhcp = false
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_network_attachment" "test" {
  server_id  = vcp_vmware_server.test.id
  network_id = vcp_vmware_network.test.id

}
```

## nic/attachmentDHCP

TestAccVmwareServerNetworkAttachment_dhcp — a network that hands out addresses.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_network" "test" {
  type        = "isolated"
  location_id = 5
  name        = "test-acc-vmw-syntax-net"
  address     = "10.240.1.0"
  mask        = 24
  enable_dhcp = true
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_network_attachment" "test" {
  server_id  = vcp_vmware_server.test.id
  network_id = vcp_vmware_network.test.id
}
```

## nic/publicInterface

TestAccVmwareServerPublicInterface_basic — a second public address.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_public_interface" "test" {
  server_id      = vcp_vmware_server.test.id
  bandwidth_mbps = 10
}
```

## nic/parallel

TestAccVmwareServerNICs_parallel — a private attachment and a public interface on one machine in one apply (locks.VmwareServer).

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_network" "test" {
  type        = "isolated"
  location_id = 5
  name        = "test-acc-vmw-syntax-net"
  address     = "10.240.1.0"
  mask        = 24
  enable_dhcp = false
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_network_attachment" "test" {
  server_id  = vcp_vmware_server.test.id
  network_id = vcp_vmware_network.test.id
  ip         = "10.232.5.10"
}

resource "vcp_vmware_server_public_interface" "test" {
  server_id      = vcp_vmware_server.test.id
  bandwidth_mbps = 10
}
```

## rules/edgeFirewallAllFields

TestAccVmwareEdgeFirewall_basic — every attribute a rule has.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.233.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_firewall" "test" {
  network_id     = vcp_vmware_network.test.id
  default_action = "deny"

  rules = [
    {
      name             = "allow-https"
      action           = "allow"
      protocol         = "tcp"
      source           = "any"
      source_port      = "any"
      destination      = "10.233.1.10"
      destination_port = "443"
    },
    {
      name     = "allow-icmp"
      action   = "allow"
      protocol = "icmp"
    },
    {
      name             = "office-ssh"
      action           = "allow"
      protocol         = "tcp"
      source           = "203.0.113.0/24"
      source_port      = "1024-65535"
      destination      = "10.233.1.0/24"
      destination_port = "22"
    },
  ]
}
```

## rules/edgeFirewallShort

TestAccVmwareEdgeFirewall_drift, _adoptsExistingRules.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.240.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_firewall" "test" {
  network_id     = vcp_vmware_network.test.id
  default_action = "allow"

  rules = [
    {
      name             = "allow-https"
      action           = "allow"
      protocol         = "tcp"
      source           = "any"
      destination      = "any"
      destination_port = "443"
    },
  ]
}
```

## rules/edgeFirewallEmpty

TestAccVmwareEdgeFirewall_basic — an empty list: the firewall stays on and default_action becomes its whole behaviour.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.240.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_firewall" "test" {
  network_id     = vcp_vmware_network.test.id
  default_action = "deny"

  rules = [
  ]
}
```

## rules/edgeNATPair

TestAccVmwareEdgeNAT_basic — one rule of each kind. original_ip is absent on purpose (NET-4).

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.233.4.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_nat" "test" {
  network_id = vcp_vmware_network.test.id

  rules = [
    {
      type            = "dnat"
      protocol        = "tcp"
      description     = "publish https"
      original_port   = "443"
      translated_ip   = "10.233.4.10"
      translated_port = "443"
    },
    {
      type        = "snat"
      protocol    = "any"
      description = "outbound"
      original_ip = "10.233.4.0/24"
    },
  ]
}
```

## rules/edgeNATSingle

TestAccVmwareEdgeNAT_growAndShrink, _drift — the starting point.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.240.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_nat" "test" {
  network_id = vcp_vmware_network.test.id

  rules = [
    {
      type            = "dnat"
      protocol        = "tcp"
      original_port   = "443"
      translated_ip   = "10.240.1.10"
      translated_port = "443"
    },
  ]
}
```

## rules/edgeNATThree

TestAccVmwareEdgeNAT_growAndShrink — grown to three: the first rule is overwritten in place, two are created.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.240.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_nat" "test" {
  network_id = vcp_vmware_network.test.id

  rules = [
    {
      type            = "dnat"
      protocol        = "tcp"
      original_port   = "443"
      translated_ip   = "10.240.1.10"
      translated_port = "443"
    },
    {
      type            = "dnat"
      protocol        = "tcp"
      original_port   = "8080"
      translated_ip   = "10.240.1.11"
      translated_port = "8080"
    },
    {
      type            = "dnat"
      protocol        = "tcp"
      original_port   = "8443"
      translated_ip   = "10.240.1.12"
      translated_port = "8443"
    },
  ]
}
```

## rules/edgeNATEmpty

An empty NAT list — every rule removed from the edge.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.240.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_nat" "test" {
  network_id = vcp_vmware_network.test.id

  rules = [
  ]
}
```

## rules/edgeParallel

TestAccVmwareEdgeRules_parallel — firewall and NAT of one network in one apply (locks.VmwareNetwork).

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.233.9.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_firewall" "test" {
  network_id     = vcp_vmware_network.test.id
  default_action = "deny"

  rules = [
    { name = "allow-https", action = "allow", protocol = "tcp", destination_port = "443" },
    { name = "allow-icmp", action = "allow", protocol = "icmp" },
  ]
}

resource "vcp_vmware_edge_nat" "test" {
  network_id = vcp_vmware_network.test.id

  rules = [
    { type = "snat", protocol = "any", original_ip = "10.233.9.0/24" },
  ]
}
```

## rules/serverFirewallFull

TestAccVmwareServerFirewall_basic — both directions, a closing deny rule.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_firewall" "test" {
  server_id = vcp_vmware_server.test.id

  rules = [
    {
      name              = "ssh-office"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      source            = "203.0.113.0/24"
      destination_port  = "22"
    },
    {
      name              = "dns-out"
      traffic_direction = "outgoing"
      action            = "allow"
      protocol          = "udp"
      destination_port  = "53"
    },
    {
      name              = "drop-the-rest"
      traffic_direction = "incoming"
      action            = "deny"
      protocol          = "any"
    },
  ]
}
```

## rules/serverFirewallOne

TestAccVmwareServerFirewall_basic (update), _drift.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_firewall" "test" {
  server_id = vcp_vmware_server.test.id

  rules = [
    {
      name              = "ssh-office"
      traffic_direction = "incoming"
      action            = "allow"
      protocol          = "tcp"
      source            = "203.0.113.0/24"
      destination_port  = "22"
    },
  ]
}
```

## rules/serverFirewallNone

An empty server firewall — every rule removed, the machine left unfiltered.

```hcl
data "vcp_vmware_locations" "all" {}

data "vcp_vmware_images" "all" {
  location_id = 5
}

locals {
  location  = one([for l in data.vcp_vmware_locations.all.locations : l if l.id == 5])
  image     = one([for i in data.vcp_vmware_images.all.images : i if i.id == 42])
  disk_type = one([for d in local.location.disk_types : d if d.is_allowed_for_system_disk])

  ram_mb         = max(1024, local.image.min_ram_mb)
  system_disk_mb = max(local.disk_type.default_size_mb, local.disk_type.min_mb, local.image.hdd_gb * 1024)
}

resource "vcp_vmware_server" "test" {
  location_id      = 5
  name             = "test-acc-vmw-syntax"
  image_id         = 42
  cpu              = 1
  ram_mb           = local.ram_mb
  system_disk_mb   = local.system_disk_mb
  system_disk_type = local.disk_type.title
}

resource "vcp_vmware_server_firewall" "test" {
  server_id = vcp_vmware_server.test.id

  rules = [
  ]
}
```

## vpn/tunnel

TestAccVmwareEdgeVPNTunnel_basic — a site-to-site tunnel on the edge of a routed network.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.234.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_vpn_tunnel" "test" {
  network_id = vcp_vmware_network.test.id
  name       = "test-acc-vmw-syntax"
  enabled    = true

  peer_endpoint      = "203.0.113.10"
  peer_identificator = "203.0.113.10"
  peer_network       = "192.168.240.0/24"

  shared_key           = "TfAccVpnSharedKey0123456789abcdef"
  encryption_type      = "aes256"
  diffie_hellman_group = "dh14"
  mtu                  = 1500
}
```

## vpn/tunnelDisabled

TestAccVmwareEdgeVPNTunnel_basic — the same tunnel with a smaller MTU, switched off.

```hcl
resource "vcp_vmware_network" "test" {
  type           = "routed"
  location_id    = 5
  name           = "test-acc-vmw-syntax"
  address        = "10.234.1.0"
  mask           = 24
  enable_dhcp    = false
  bandwidth_mbps = 20
}

resource "vcp_vmware_edge_vpn_tunnel" "test" {
  network_id = vcp_vmware_network.test.id
  name       = "test-acc-vmw-syntax"
  enabled    = false

  peer_endpoint      = "203.0.113.10"
  peer_identificator = "203.0.113.10"
  peer_network       = "192.168.240.0/24"

  shared_key           = "TfAccVpnSharedKey0123456789abcdef"
  encryption_type      = "aes256"
  diffie_hellman_group = "dh14"
  mtu                  = 1400
}
```

