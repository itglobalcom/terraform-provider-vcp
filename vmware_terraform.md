# VMware в Terraform-провайдере: что делаем

Дата: 2026-08-12. Техническое ревью — в `VMWARE_REVIEW.md`.

## Что есть сейчас

Ресурсы `vcp_vmware_server` и `vcp_vmware_network` (isolated/routed/public) плюс справочные и
инвентарные data sources. Полностью закрыт один сценарий — **отдельная VM с публичным IP**:
создание со всеми опциями, ресайз и переименование без пересоздания, import, справочники вместо
хардкода id.

Дальше начинается панель: сеть между машинами, второй диск, edge — ничего из этого из Terraform
не описывается.

## 1. Сети сервера

**VMware-сервер всегда создаётся с первичным публичным интерфейсом**: если `public_network_id`
не задан, платформа сама подберёт публичную shared-сеть. VM без интерфейса не бывает, поэтому
первичный интерфейс остаётся свойством сервера — как WAN у `vcp_gateway`. (В vStack иначе: там
сервер создаётся с нулём NIC-ов, и все интерфейсы вынесены в ресурсы.)

| Что | Чем управляется |
|---|---|
| Первичный публичный интерфейс | поля сервера `public_network_id` / `network_bandwidth_mbps`, полоса становится изменяемой in-place |
| Дополнительные публичные IP | `vcp_vmware_server_public_interface` — каждый тарифицируется |
| Клиентские сети (isolated/routed) | `vcp_vmware_server_network_attachment` |

```hcl
resource "vcp_vmware_server" "web" {
  # ...
  network_bandwidth_mbps = 100        # первичный интерфейс, меняется без пересоздания
}

resource "vcp_vmware_server_network_attachment" "db" {
  server_id  = vcp_vmware_server.db.id
  network_id = vcp_vmware_network.private.id
  ip         = "10.100.0.10"          # опционально
}
```

- при заданном `public_network_id` полоса берётся из самой сети, а `network_bandwidth_mbps`
  игнорируется — объявляем поля взаимоисключающими;
- отключение NIC на включённой VM зависит от `nic_hot_remove` образа — переводим отказ API в
  понятное «выключите VM в панели».

## 2. Диски

Как в vStack: вложенный список `volumes` внутри сервера, включая загрузочный. `number` —
стабильный ключ, который выбирает пользователь; благодаря ему переименование диска остаётся
переименованием, а не «удалить и создать заново». Поля `system_disk_mb` / `system_disk_type`
убираем — системный диск живёт в boot-элементе.

```hcl
resource "vcp_vmware_server" "db" {
  # ...
  volumes = [
    { number = 0, name = "boot",    size_mb = 51200,  disk_type = local.ssd_type },
    { number = 1, name = "db-data", size_mb = 512000, disk_type = local.ssd_type },
  ]
}
```

- boot нельзя удалить или переименовать — понятная ошибка на plan;
- расширение размера in-place, уменьшение — ошибка на plan;
- смена `disk_type`: у boot — пересоздание всего сервера, у остальных — только тома;
- `VolumesNumberChangeModifier` из `vcp_server` выносим в общий пакет, а не копируем.

## 3. Edge

Возможности зависят от типа сети — провайдер проверяет применимость на plan:

| | routed | public |
|---|---|---|
| `edge_bandwidth_mbps` (атрибут сети) | ✅ | — |
| `vcp_vmware_edge_firewall` | ✅ | ✅ |
| `vcp_vmware_edge_nat` | ✅ | — |
| `vcp_vmware_edge_vpn_tunnel` | ✅ | ✅ |

Операции над одним edge сериализуются (per-network lock): платформа перекладывает в vCloud весь
набор объектов даже при правке одного правила.

### Firewall

Ресурс владеет всем набором правил — конфиг является полной картиной, ручные правки в панели
выправляются следующим apply. Отдельного флага `enabled` у ресурса нет: включённость выражается
существованием ресурса. `default_action` обязателен, это режим работы — `deny` даёт белый
список, `allow` чёрный. Порядок элементов = порядок применения, у каждого правила свой
`enabled`.

```hcl
resource "vcp_vmware_edge_firewall" "app" {
  network_id     = vcp_vmware_network.app.id
  default_action = "deny"

  rules = [
    {
      name             = "allow-https"
      action           = "allow"
      protocol         = "tcp"
      source           = "any"
      destination      = "10.200.0.10"
      destination_port = "443"
    },
    {
      name        = "allow-icmp"
      action      = "allow"
      protocol    = "icmp"
      source      = "any"
      destination = "any"
      enabled     = false            # выключено, но не потеряно
    },
  ]
}
```

### NAT

Модель та же, но набор применяется не одним вызовом, а по правилу: при отказе на середине edge
остаётся частично применённым — провайдер записывает фактическое состояние и сообщает, на каком
правиле встал. У элементов есть `number` — стабильный ключ, как у дисков, чтобы вставка правила
не переписывала соседние. Приоритетом NAT-правил API не управляет, поэтому позиция элемента ни
на что не влияет (тип всё равно список: Set не уживается с `Optional + Computed` полями внутри
элементов).

```hcl
resource "vcp_vmware_edge_nat" "app" {
  network_id = vcp_vmware_network.app.id

  rules = [
    {
      number          = 0
      type            = "dnat"       # снаружи внутрь: original_* — внешняя сторона
      protocol        = "tcp"
      original_port   = "443"        # original_ip опционален — подставится адрес edge
      translated_ip   = "10.200.0.10"
      translated_port = "443"
    },
    {
      number        = 1
      type          = "snat"         # изнутри наружу: original_ip — внутренний источник
      protocol      = "any"
      original_ip   = "10.200.0.0/24"
      translated_ip = "203.0.113.5"  # внешний адрес edge, можно опустить
    },
  ]
}
```

Адреса, которыми владеет edge (`original_ip` у DNAT, `translated_ip` у SNAT), — `Optional +
Computed`: их можно не указывать, фактическое значение приезжает из API и не создаёт вечный
diff. Неверно указанный адрес отвергается ошибкой, а не подменяется молча.

### VPN

Site-to-site IPsec: туннель связывает эту сеть с одной подсетью на удалённой стороне. Здесь
ресурс на туннель — это не правило политики, а именованное подключение со своим секретом (имя
уникально в пределах сети). Локальную сторону задаёт платформа: `local_ip`, `local_id`,
`local_subnets` только Computed, как и `digest_algorithm`, версия IKE и режим аутентификации.

```hcl
resource "vcp_vmware_edge_vpn_tunnel" "office" {
  network_id = vcp_vmware_network.app.id
  name       = "office"

  peer_endpoint      = "203.0.113.10"      # IPv4, не hostname
  peer_identificator = "203.0.113.10"      # IPv4
  peer_network       = "192.168.10.0/24"   # ровно одна подсеть на туннель

  shared_key              = var.vpn_psk    # sensitive
  encryption_type         = "aes256"       # aes | aes256 | triple_des | aes_gcm
  diffie_hellman_group    = "dh14"         # dh2 | dh5 | dh14 | dh15 | dh16
  mtu                     = 1500
  perfect_forward_secrecy = true
  enabled                 = true
}
```

Ограничения платформы — проверяем в пределах конфига, ошибки API переводим внятно: до 50
туннелей на сеть; имя и `peer_network` уникальны в сети; туннели с одним peer обязаны иметь
одинаковый `shared_key`; `peer_endpoint` не может совпадать с внешним адресом edge или шлюзом
сети; на сетях NSX-T VPN недоступен. `shared_key` API не возвращает — живёт в state как
sensitive, смена в панели дрейфом не считается.

## 4. Firewall сервера

Та же модель, что у edge-firewall: весь набор правил в одном ресурсе, порядок = приоритет.
Режима `default_action` здесь нет, только правила.

```hcl
resource "vcp_vmware_server_firewall" "web" {
  server_id = vcp_vmware_server.web.id

  rules = [
    {
      name              = "ssh-office"
      traffic_direction = "in"
      action            = "allow"
      protocol          = "tcp"
      source            = "203.0.113.0/24"
      destination_port  = "22"
    },
  ]
}
```

## 5. Справочники

Приводим к новому контракту: `vmware_disk_types` и `vmware_storage_profiles` уходят (типы дисков
переезжают в `vmware_locations` — `locations[].disk_types[]`), GPU заказывается одним полем
вместо тройки `model_id`/`vram_mb`/`card_count`:

```hcl
resource "vcp_vmware_server" "ml" {
  # ...
  gpu_profile_id = data.vcp_vmware_gpu_profiles.all.profiles[0].id   # смена = пересоздание VM
}
```

## 6. Мелкие правки

- `computer_name`: бэк хранит hostname в верхнем регистре — сравниваем без учёта регистра,
  чтобы `web-01` не ронял apply; в state остаётся значение из конфига.
- Поля не своего типа сети (`bandwidth_mbps` и `edge_bandwidth_mbps` у isolated, `capacity` у
  isolated/routed, `address`/`mask`/`enable_dhcp` у public) — понятная ошибка на
  `terraform validate` вместо молчаливого игнорирования.
- `ssh_keys` → `ssh_key_ids`, List → Set (как в `vcp_server`): сейчас перестановка ключей
  планируется как пересоздание сервера, хотя для API порядок не значим.
- Ресайз включённой VM ограничен флагами образа `cpu_hot_add` / `memory_hot_add` — объясняем
  причину отказа человеческим текстом.
- Поиск сервера по `name` в data source: сейчас только по id, который надо смотреть в панели.
- В доках: чек-лист import и предупреждение, что смена `image_id` — пересоздание VM с потерей
  данных.

## 7. Импорт и приёмка

Ломающие правки схем делаем без deprecated-периода: VMware-ресурсы не выпускались — на `main`
их нет, единственный тег `v0.1.1` их не содержит.

| Ресурс | ID для import |
|---|---|
| `vcp_vmware_server_network_attachment` | `<server_id>:<nic_id>` |
| `vcp_vmware_server_public_interface` | `<server_id>:<nic_id>` |
| `vcp_vmware_server_firewall` | `<server_id>` |
| `vcp_vmware_edge_firewall` | `<network_id>` |
| `vcp_vmware_edge_nat` | `<network_id>` |
| `vcp_vmware_edge_vpn_tunnel` | `<network_id>:<tunnel_id>` |

Диски отдельного импорта не имеют — приезжают вместе с сервером. При импорте NAT провайдер
проставляет `number` по порядку выдачи API, и пользователь обязан повторить эти значения в
конфиге — иначе первый же plan предложит пересоздать правила; пишем это в доку ресурса.

Приёмка: сейчас у vmware-пакета только smoke-тесты схем. На каждый новый ресурс —
acceptance-тест (create → import → update → destroy) и sweeper, как в vStack-части; unit-тесты —
на мапперы и на обещанные пользователю валидации (взаимоисключающие поля, применимость edge к
типу сети, правила boot-элемента).

## Порядок

1. Справочники и мелкие правки (§5–6) — пока у схем нет пользователей.
2. Сети сервера и диски (§1–2) — закрывают основной разрыв.
3. Edge и firewall сервера (§3–4).
