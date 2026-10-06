package server_backup

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/itglobalcom/terraform-provider-vcp/internal/locks"
	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ resource.Resource                   = &backupResource{}
	_ resource.ResourceWithConfigure      = &backupResource{}
	_ resource.ResourceWithImportState    = &backupResource{}
	_ resource.ResourceWithValidateConfig = &backupResource{}
)

func NewResource() resource.Resource { return &backupResource{} }

type backupResource struct {
	client *sdk.CloudClient
}

// backupModel — model of the vcp_server_backup resource. A nil rule is a null
// object: the rule is off.
type backupModel struct {
	ID       types.String      `tfsdk:"id"`
	ServerID types.String      `tfsdk:"server_id"`
	Hour     types.Int64       `tfsdk:"hour"`
	Minute   types.Int64       `tfsdk:"minute"`
	Daily    *dailyRuleModel   `tfsdk:"daily"`
	Weekly   *weeklyRuleModel  `tfsdk:"weekly"`
	Monthly  *monthlyRuleModel `tfsdk:"monthly"`
}

type dailyRuleModel struct {
	Keep            types.Int64 `tfsdk:"keep"`
	BackupStorageID types.Int64 `tfsdk:"backup_storage_id"`
}

type weeklyRuleModel struct {
	Keep            types.Int64  `tfsdk:"keep"`
	BackupStorageID types.Int64  `tfsdk:"backup_storage_id"`
	Weekday         types.String `tfsdk:"weekday"`
}

type monthlyRuleModel struct {
	Keep            types.Int64  `tfsdk:"keep"`
	BackupStorageID types.Int64  `tfsdk:"backup_storage_id"`
	DayOfMonth      types.String `tfsdk:"day_of_month"`
}

// ruleNames are the three optional rules; at least one has to be set.
var ruleNames = []string{"daily", "weekly", "monthly"}

// daysOfMonth are the values day_of_month takes: "1" to "28" and "last".
func daysOfMonth() []string {
	days := make([]string, 0, 29)
	for d := 1; d <= 28; d++ {
		days = append(days, strconv.Itoa(d))
	}
	return append(days, entities.BackupDayOfMonthLast)
}

// weekdays are the values weekday takes.
func weekdays() []string {
	return []string{
		string(entities.BackupWeekdayMonday),
		string(entities.BackupWeekdayTuesday),
		string(entities.BackupWeekdayWednesday),
		string(entities.BackupWeekdayThursday),
		string(entities.BackupWeekdayFriday),
		string(entities.BackupWeekdaySaturday),
		string(entities.BackupWeekdaySunday),
	}
}

func (r *backupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_backup"
}

// ruleAttributes are the attributes every rule has; extra adds the rule's own.
func ruleAttributes(rule string, extra map[string]schema.Attribute) map[string]schema.Attribute {
	attrs := map[string]schema.Attribute{
		"keep": schema.Int64Attribute{
			MarkdownDescription: fmt.Sprintf("How many `%s` copies are kept; the oldest is deleted when a new one "+
				"is taken. At most `limits.%s.max_keep` of `vcp_server_backup_storages`.", rule, rule),
			Required:   true,
			Validators: []validator.Int64{int64validator.AtLeast(1)},
		},
		"backup_storage_id": schema.Int64Attribute{
			MarkdownDescription: "ID of the storage the copies are kept in — an `id` from " +
				"`vcp_server_backup_storages`. Changing it is applied in place.",
			Required:   true,
			Validators: []validator.Int64{int64validator.AtLeast(1)},
		},
	}
	for name, attr := range extra {
		attrs[name] = attr
	}
	return attrs
}

func (r *backupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Enables the backup service of a server and manages its schedule: the time copies " +
			"are taken at and up to three rules — `daily`, `weekly` and `monthly` — each with the number of copies " +
			"it keeps and the storage it keeps them in. A server has one schedule, so declare at most one " +
			"`vcp_server_backup` per server. Every change is applied in place; only a different `server_id` " +
			"replaces the resource.\n\n" +
			"The schedule has to fit the partner's limits (`limits` of `vcp_server_backup_storages`); the API " +
			"refuses one that does not.\n\n" +
			"~> Destroying this resource disables the backup service of the server, and **the server's copies are " +
			"deleted with it**. To stop managing the schedule without disabling it, drop the resource from state " +
			"(`terraform state rm vcp_server_backup.<name>`) instead of destroying it.\n\n" +
			"~> A server whose backup service is on the legacy backup model has no schedule and cannot be managed " +
			"or imported by this resource; manage its backups in the panel.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `server_id` — one schedule per server.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"server_id": schema.StringAttribute{
				MarkdownDescription: "ID of the server whose backups are scheduled. Changing this forces a new resource.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"hour": schema.Int64Attribute{
				MarkdownDescription: "Hour the copies start at, `0` to `23`. It has to be within " +
					"`limits.schedule_window_from_hour` and `limits.schedule_window_to_hour` of " +
					"`vcp_server_backup_storages`.",
				Required:   true,
				Validators: []validator.Int64{int64validator.Between(0, 23)},
			},
			"minute": schema.Int64Attribute{
				MarkdownDescription: "Minute of `hour` the copies start at, `0` to `59`.",
				Required:            true,
				Validators:          []validator.Int64{int64validator.Between(0, 59)},
			},
			"daily": schema.SingleNestedAttribute{
				MarkdownDescription: "Daily rule: a copy every day. Omit it to take no daily copies. At least one " +
					"of `daily`, `weekly` and `monthly` is required.",
				Optional:   true,
				Attributes: ruleAttributes("daily", nil),
			},
			"weekly": schema.SingleNestedAttribute{
				MarkdownDescription: "Weekly rule: a copy on one day of the week. Omit it to take no weekly copies.",
				Optional:            true,
				Attributes: ruleAttributes("weekly", map[string]schema.Attribute{
					"weekday": schema.StringAttribute{
						MarkdownDescription: "Day of the week the copy is taken on: `\"monday\"`, `\"tuesday\"`, " +
							"`\"wednesday\"`, `\"thursday\"`, `\"friday\"`, `\"saturday\"` or `\"sunday\"`.",
						Required:   true,
						Validators: []validator.String{stringvalidator.OneOf(weekdays()...)},
					},
				}),
			},
			"monthly": schema.SingleNestedAttribute{
				MarkdownDescription: "Monthly rule: a copy on one day of the month. Omit it to take no monthly copies.",
				Optional:            true,
				Attributes: ruleAttributes("monthly", map[string]schema.Attribute{
					"day_of_month": schema.StringAttribute{
						MarkdownDescription: "Day of the month the copy is taken on: `\"1\"` to `\"28\"`, or `\"last\"` " +
							"for the last day of the month.",
						Required:   true,
						Validators: []validator.String{stringvalidator.OneOf(daysOfMonth()...)},
					},
				}),
			},
		},
	}
}

func (r *backupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sdk.CloudClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *sdk.CloudClient, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

// ValidateConfig refuses a schedule without any rule at plan time; the API
// would refuse it only once the apply has started. A rule that is not known
// yet may turn out to be set, so it skips the check.
func (r *backupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	for _, name := range ruleNames {
		var rule types.Object
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(name), &rule)...)
		if resp.Diagnostics.HasError() || !rule.IsNull() {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(path.Root("daily"), "Backup Schedule Has No Rule",
		"Set at least one of daily, weekly and monthly: a schedule without a rule takes no copies.")
}

func (r *backupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan backupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := plan.ServerID.ValueString()
	defer locks.Server(serverID)()

	tflog.Info(ctx, "Enabling server backup", map[string]any{"server_id": serverID})
	before, snapErr := r.client.GetServerBackup(ctx, serverID)

	backup, err := r.client.EnableServerBackupAndWait(ctx, serverID, expandSchedule(plan))
	if err != nil {
		resp.Diagnostics.AddError("Error Enabling Server Backup",
			fmt.Sprintf("Could not enable the backup service of server %s: %s", serverID, err.Error()))
		if snapErr == nil && !before.Enabled {
			r.recordEnabledBackup(ctx, serverID, resp)
		}
		return
	}

	state, ok := mapServerBackup(serverID, backup, &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// recordEnabledBackup writes the service into state when the enabling request
// went through but its wait failed, so the next apply does not enable it again.
func (r *backupResource) recordEnabledBackup(ctx context.Context, serverID string, resp *resource.CreateResponse) {
	backup, err := r.client.GetServerBackup(ctx, serverID)
	if err != nil || !backup.Enabled {
		return
	}
	var mapDiags diag.Diagnostics
	state, ok := mapServerBackup(serverID, backup, &mapDiags)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	resp.Diagnostics.AddWarning("Backup Recorded Despite Error",
		fmt.Sprintf("The backup service of server %s is enabled and was recorded in state; the resource is tainted and the next apply replaces it.", serverID))
}

func (r *backupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state backupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := state.ServerID.ValueString()
	backup, err := r.client.GetServerBackup(ctx, serverID)
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Server Backup",
			fmt.Sprintf("Could not read the backup service of server %s: %s", serverID, err.Error()))
		return
	}
	if !backup.Enabled {
		resp.State.RemoveResource(ctx)
		return
	}

	newState, ok := mapServerBackup(serverID, backup, &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newState)...)
}

func (r *backupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan backupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := plan.ServerID.ValueString()
	defer locks.Server(serverID)()

	tflog.Info(ctx, "Updating server backup schedule", map[string]any{"server_id": serverID})
	backup, err := r.client.UpdateServerBackupAndWait(ctx, serverID, expandSchedule(plan))
	if err != nil {
		resp.Diagnostics.AddError("Error Updating Server Backup",
			fmt.Sprintf("Could not update the backup schedule of server %s: %s", serverID, err.Error()))
		return
	}

	state, ok := mapServerBackup(serverID, backup, &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *backupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state backupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := state.ServerID.ValueString()
	defer locks.Server(serverID)()

	backup, err := r.client.GetServerBackup(ctx, serverID)
	switch {
	case err != nil && sdk.IsNotFound(err):
		tflog.Info(ctx, "Server already deleted, no backup service to disable", map[string]any{"server_id": serverID})
		return
	case err == nil && !backup.Enabled:
		tflog.Info(ctx, "Server backup already disabled", map[string]any{"server_id": serverID})
		return
	}

	tflog.Info(ctx, "Disabling server backup", map[string]any{"server_id": serverID})
	if err := r.client.DisableServerBackupAndWait(ctx, serverID); err != nil {
		if sdk.IsNotFound(err) {
			tflog.Info(ctx, "Server already deleted, no backup service to disable", map[string]any{"server_id": serverID})
			return
		}
		resp.Diagnostics.AddError("Error Disabling Server Backup",
			fmt.Sprintf("Could not disable the backup service of server %s: %s", serverID, err.Error()))
	}
}

// ImportState takes the server id; Read fills in the schedule.
func (r *backupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("server_id"), req, resp)
}

// expandSchedule builds the API schedule from the plan; a null rule is left
// out of the request.
func expandSchedule(m backupModel) *entities.BackupSchedule {
	s := &entities.BackupSchedule{
		Hour:   int(m.Hour.ValueInt64()),
		Minute: int(m.Minute.ValueInt64()),
	}
	if d := m.Daily; d != nil {
		s.Daily = &entities.BackupRule{
			Keep:            int(d.Keep.ValueInt64()),
			BackupStorageID: int(d.BackupStorageID.ValueInt64()),
		}
	}
	if w := m.Weekly; w != nil {
		s.Weekly = &entities.BackupWeeklyRule{
			BackupRule: entities.BackupRule{
				Keep:            int(w.Keep.ValueInt64()),
				BackupStorageID: int(w.BackupStorageID.ValueInt64()),
			},
			Weekday: entities.BackupWeekday(w.Weekday.ValueString()),
		}
	}
	if mo := m.Monthly; mo != nil {
		s.Monthly = &entities.BackupMonthlyRule{
			BackupRule: entities.BackupRule{
				Keep:            int(mo.Keep.ValueInt64()),
				BackupStorageID: int(mo.BackupStorageID.ValueInt64()),
			},
			DayOfMonth: mo.DayOfMonth.ValueString(),
		}
	}
	return s
}

// mapServerBackup transfers the state of an enabled service into the model. A
// disabled service and one without a schedule have no model: the first is not
// what an apply left behind, the second is the legacy backup model, whose
// copies are not governed by a schedule this resource could hold.
func mapServerBackup(serverID string, b *entities.ServerBackup, diags *diag.Diagnostics) (backupModel, bool) {
	if !b.Enabled {
		diags.AddError("Server Backup Not Enabled",
			fmt.Sprintf("The backup service of server %s is not enabled after the apply finished.", serverID))
		return backupModel{}, false
	}
	s := b.Schedule
	if s == nil {
		diags.AddError("Server Backup On The Legacy Model",
			fmt.Sprintf("Server %s has its backups on the legacy backup model, which has no schedule, so "+
				"vcp_server_backup cannot manage them. Manage the server's backups in the panel; if the "+
				"resource is already in state, remove it with `terraform state rm`.", serverID))
		return backupModel{}, false
	}

	m := backupModel{
		ID:       types.StringValue(serverID),
		ServerID: types.StringValue(serverID),
		Hour:     types.Int64Value(int64(s.Hour)),
		Minute:   types.Int64Value(int64(s.Minute)),
	}
	if d := s.Daily; d != nil {
		m.Daily = &dailyRuleModel{
			Keep:            types.Int64Value(int64(d.Keep)),
			BackupStorageID: types.Int64Value(int64(d.BackupStorageID)),
		}
	}
	if w := s.Weekly; w != nil {
		m.Weekly = &weeklyRuleModel{
			Keep:            types.Int64Value(int64(w.Keep)),
			BackupStorageID: types.Int64Value(int64(w.BackupStorageID)),
			Weekday:         types.StringValue(string(w.Weekday)),
		}
	}
	if mo := s.Monthly; mo != nil {
		m.Monthly = &monthlyRuleModel{
			Keep:            types.Int64Value(int64(mo.Keep)),
			BackupStorageID: types.Int64Value(int64(mo.BackupStorageID)),
			DayOfMonth:      types.StringValue(mo.DayOfMonth),
		}
	}
	return m, true
}
