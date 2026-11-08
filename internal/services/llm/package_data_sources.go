package llm

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ datasource.DataSourceWithConfigure = &packageDataSource{}
	_ datasource.DataSourceWithConfigure = &packagePlansDataSource{}
)

const taxLabelDescription = "Tax caption of the prices: `nds_excluded` or `vat_excluded`; null when usage is in credits only."

// ----------------------------------------------------------------------
// vcp_llm_package
// ----------------------------------------------------------------------

// NewPackageDataSource returns the vcp_llm_package data source.
func NewPackageDataSource() datasource.DataSource {
	return &packageDataSource{}
}

type packageDataSource struct {
	client *sdk.CloudClient
}

type currentPackageModel struct {
	Package                *packageModel     `tfsdk:"package"`
	Usage                  packageUsageModel `tfsdk:"usage"`
	PricePerCredit         types.Float64     `tfsdk:"price_per_credit"`
	Currency               types.String      `tfsdk:"currency"`
	TaxLabel               types.String      `tfsdk:"tax_label"`
	PackagesDisabled       types.Bool        `tfsdk:"packages_disabled"`
	ExhaustedPackageMode   types.String      `tfsdk:"exhausted_package_mode"`
	PayAsYouGoLimitCredits types.Float64     `tfsdk:"pay_as_you_go_limit_credits"`
	Stop                   *packageStopModel `tfsdk:"stop"`
}

type packageModel struct {
	ID                     types.Int64   `tfsdk:"id"`
	TechTitle              types.String  `tfsdk:"tech_title"`
	VolumeCredits          types.Float64 `tfsdk:"volume_credits"`
	DiscountPercent        types.Float64 `tfsdk:"discount_percent"`
	Price                  types.Float64 `tfsdk:"price"`
	FullMonthVolumeCredits types.Float64 `tfsdk:"full_month_volume_credits"`
	FullMonthPrice         types.Float64 `tfsdk:"full_month_price"`
	PeriodFrom             types.String  `tfsdk:"period_from"`
	PeriodTo               types.String  `tfsdk:"period_to"`
	AutoRenew              types.Bool    `tfsdk:"auto_renew"`
	State                  types.String  `tfsdk:"state"`
}

type packageUsageModel struct {
	PeriodFrom           types.String  `tfsdk:"period_from"`
	PeriodTo             types.String  `tfsdk:"period_to"`
	TotalCredits         types.Float64 `tfsdk:"total_credits"`
	PackageVolumeCredits types.Float64 `tfsdk:"package_volume_credits"`
	InPackageCredits     types.Float64 `tfsdk:"in_package_credits"`
	OverPackageCredits   types.Float64 `tfsdk:"over_package_credits"`
}

type packageStopModel struct {
	StoppedAt types.String `tfsdk:"stopped_at"`
	Reason    types.String `tfsdk:"reason"`
	State     types.String `tfsdk:"state"`
}

func currentPackageStateFrom(p *entities.LLMCurrentPackage) *currentPackageModel {
	out := &currentPackageModel{
		Usage: packageUsageModel{
			PeriodFrom:           types.StringValue(p.Usage.PeriodFrom),
			PeriodTo:             types.StringValue(p.Usage.PeriodTo),
			TotalCredits:         types.Float64Value(p.Usage.TotalCredits),
			PackageVolumeCredits: optFloat(p.Usage.PackageVolumeCredits),
			InPackageCredits:     types.Float64Value(p.Usage.InPackageCredits),
			OverPackageCredits:   types.Float64Value(p.Usage.OverPackageCredits),
		},
		PricePerCredit:         optFloat(p.PricePerCredit),
		Currency:               optNonEmptyString(p.Currency),
		TaxLabel:               optTaxLabel(p.TaxLabel),
		PackagesDisabled:       types.BoolValue(p.PackagesDisabled),
		ExhaustedPackageMode:   types.StringValue(string(p.ExhaustedPackageMode)),
		PayAsYouGoLimitCredits: optFloat(p.PayAsYouGoLimitCredits),
	}
	if pkg := p.Package; pkg != nil {
		out.Package = &packageModel{
			ID:                     types.Int64Value(int64(pkg.ID)),
			TechTitle:              types.StringValue(pkg.TechTitle),
			VolumeCredits:          types.Float64Value(pkg.VolumeCredits),
			DiscountPercent:        optFloat(pkg.DiscountPercent),
			Price:                  optFloat(pkg.Price),
			FullMonthVolumeCredits: optFloat(pkg.FullMonthVolumeCredits),
			FullMonthPrice:         optFloat(pkg.FullMonthPrice),
			PeriodFrom:             types.StringValue(pkg.PeriodFrom),
			PeriodTo:               types.StringValue(pkg.PeriodTo),
			AutoRenew:              types.BoolValue(pkg.AutoRenew),
			State:                  types.StringValue(string(pkg.State)),
		}
	}
	if stop := p.Stop; stop != nil {
		out.Stop = &packageStopModel{
			StoppedAt: types.StringValue(stop.StoppedAt),
			Reason:    types.StringValue(string(stop.Reason)),
			State:     types.StringValue(string(stop.State)),
		}
	}
	return out
}

func (d *packageDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_llm_package"
}

func (d *packageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: description, Computed: true}
	}
	computedFloat := func(description string) schema.Float64Attribute {
		return schema.Float64Attribute{MarkdownDescription: description, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the LLM credit package of the project: the current package, the spend of the " +
			"open period, the price of a credit and the stop of the service. Readable before the project is " +
			"connected to the LLM service. Packages are bought in the panel. A project whose usage is in " +
			"credits only has no prices: `price_per_credit`, `currency`, `tax_label` and the prices of the " +
			"package are null.",
		Attributes: map[string]schema.Attribute{
			"package": schema.SingleNestedAttribute{
				MarkdownDescription: "Current package; null when the project has none.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"id":                        schema.Int64Attribute{MarkdownDescription: "Package ID.", Computed: true},
					"tech_title":                computedString("Plan of the package, as `tech_title` of `vcp_llm_package_plans`."),
					"volume_credits":            computedFloat("Credits of the package period."),
					"discount_percent":          computedFloat("Discount of the plan, percent; null when the package has no price."),
					"price":                     computedFloat("Price of the package period; null when usage is in credits only."),
					"full_month_volume_credits": computedFloat("Credits of a full month, when the period is shorter."),
					"full_month_price":          computedFloat("Price of a full month, when the period is shorter."),
					"period_from":               computedString("First day of the package period, `YYYY-MM-DD`."),
					"period_to":                 computedString("Last day of the package period, `YYYY-MM-DD`."),
					"auto_renew":                schema.BoolAttribute{MarkdownDescription: "Whether the package renews for the next month.", Computed: true},
					"state":                     computedString("Package state: `scheduled`, `active`, `finished`, `replaced` or `cancelled`."),
				},
			},
			"usage": schema.SingleNestedAttribute{
				MarkdownDescription: "Spend of the open period.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"period_from":            computedString("First day of the period, `YYYY-MM-DD`."),
					"period_to":              computedString("Day after the last day of the period, `YYYY-MM-DD`."),
					"total_credits":          computedFloat("Credits spent over the period."),
					"package_volume_credits": computedFloat("Credits of the package, when there is one."),
					"in_package_credits":     computedFloat("Credits spent within the package."),
					"over_package_credits":   computedFloat("Credits spent over the package."),
				},
			},
			"price_per_credit":            computedFloat("Price of one credit for the project; null when usage is in credits only."),
			"currency":                    computedString("Currency code of the prices, e.g. `RUB`; null when usage is in credits only."),
			"tax_label":                   computedString(taxLabelDescription),
			"packages_disabled":           schema.BoolAttribute{MarkdownDescription: "Whether packages are not offered to the project.", Computed: true},
			"exhausted_package_mode":      computedString("What happens once the current package is used up: `stop` or `continue_pay_as_you_go`; `continue_pay_as_you_go` when there is no package."),
			"pay_as_you_go_limit_credits": computedFloat("Credits the project may spend over the package, in the `continue_pay_as_you_go` mode."),
			"stop": schema.SingleNestedAttribute{
				MarkdownDescription: "Stop of the service by the package, the pay-as-you-go limit or the project quota; null when " +
					"the service is not stopped.",
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"stopped_at": computedString("When the service was stopped, RFC 3339."),
					"reason":     computedString("Why: `package_exhausted`, `pay_as_you_go_limit_exhausted` or `project_quota_exhausted`. New reasons may be added over time."),
					"state":      computedString("Stop state: `stopped`, `starting` or `started`."),
				},
			},
		},
	}
}

func (d *packageDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := clientFrom(req.ProviderData, &resp.Diagnostics); client != nil {
		d.client = client
	}
}

func (d *packageDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	current, err := d.client.GetLLMPackage(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read LLM package", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, currentPackageStateFrom(current))...)
}

// ----------------------------------------------------------------------
// vcp_llm_package_plans
// ----------------------------------------------------------------------

// NewPackagePlansDataSource returns the vcp_llm_package_plans data source.
func NewPackagePlansDataSource() datasource.DataSource {
	return &packagePlansDataSource{}
}

type packagePlansDataSource struct {
	client *sdk.CloudClient
}

type packagePlansModel struct {
	Plans []packagePlanModel `tfsdk:"plans"`
}

type packagePlanModel struct {
	ID              types.Int64          `tfsdk:"id"`
	Source          types.String         `tfsdk:"source"`
	TechTitle       types.String         `tfsdk:"tech_title"`
	Description     types.String         `tfsdk:"description"`
	VolumeCredits   types.Float64        `tfsdk:"volume_credits"`
	DiscountPercent types.Float64        `tfsdk:"discount_percent"`
	Price           types.Float64        `tfsdk:"price"`
	Currency        types.String         `tfsdk:"currency"`
	TaxLabel        types.String         `tfsdk:"tax_label"`
	CurrentMonth    *planMonthTermsModel `tfsdk:"current_month"`
}

type planMonthTermsModel struct {
	PeriodFrom    types.String  `tfsdk:"period_from"`
	PeriodTo      types.String  `tfsdk:"period_to"`
	VolumeCredits types.Float64 `tfsdk:"volume_credits"`
	Price         types.Float64 `tfsdk:"price"`
}

func packagePlansStateFrom(plans []entities.LLMPackagePlan) *packagePlansModel {
	out := &packagePlansModel{Plans: make([]packagePlanModel, len(plans))}
	for i, p := range plans {
		description := types.StringNull()
		if p.Description != "" {
			description = types.StringValue(p.Description)
		}
		plan := packagePlanModel{
			ID:              types.Int64Value(int64(p.ID)),
			Source:          types.StringValue(string(p.Source)),
			TechTitle:       types.StringValue(p.TechTitle),
			Description:     description,
			VolumeCredits:   types.Float64Value(p.VolumeCredits),
			DiscountPercent: optFloat(p.DiscountPercent),
			Price:           optFloat(p.Price),
			Currency:        optString(p.Currency),
			TaxLabel:        optTaxLabel(p.TaxLabel),
		}
		if m := p.CurrentMonth; m != nil {
			plan.CurrentMonth = &planMonthTermsModel{
				PeriodFrom:    types.StringValue(m.PeriodFrom),
				PeriodTo:      types.StringValue(m.PeriodTo),
				VolumeCredits: types.Float64Value(m.VolumeCredits),
				Price:         optFloat(m.Price),
			}
		}
		out.Plans[i] = plan
	}
	return out
}

func (d *packagePlansDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_llm_package_plans"
}

func (d *packagePlansDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the LLM credit packages available to the project: the packages of the partner tariff " +
			"and the individual packages of the project. Readable before the project is connected to the LLM " +
			"service. A project whose usage is in credits only has no prices: `price`, `discount_percent`, " +
			"`currency` and `tax_label` are null.",
		Attributes: map[string]schema.Attribute{
			"plans": schema.ListNestedAttribute{
				MarkdownDescription: "Package plans.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.Int64Attribute{
							MarkdownDescription: "Package number within its `source`; unique only together with `source`: a package " +
								"of the partner tariff and an individual package of the project may share it.",
							Computed: true,
						},
						"source": schema.StringAttribute{
							MarkdownDescription: "Where the package comes from: `catalog` — a package of the partner tariff, " +
								"`individual` — an individual package of the project.",
							Computed: true,
						},
						"tech_title":       schema.StringAttribute{MarkdownDescription: "Plan name.", Computed: true},
						"description":      schema.StringAttribute{MarkdownDescription: "Plan description, in English.", Computed: true},
						"volume_credits":   schema.Float64Attribute{MarkdownDescription: "Credits of a month.", Computed: true},
						"discount_percent": schema.Float64Attribute{MarkdownDescription: "Discount of the plan, percent; null when the plan has no price.", Computed: true},
						"price":            schema.Float64Attribute{MarkdownDescription: "Price of a month; null when usage is in credits only.", Computed: true},
						"currency":         schema.StringAttribute{MarkdownDescription: "Currency code of the price, e.g. `RUB`; null when usage is in credits only.", Computed: true},
						"tax_label":        schema.StringAttribute{MarkdownDescription: taxLabelDescription, Computed: true},
						"current_month": schema.SingleNestedAttribute{
							MarkdownDescription: "Terms for the rest of the current month, when bought now.",
							Computed:            true,
							Attributes: map[string]schema.Attribute{
								"period_from":    schema.StringAttribute{MarkdownDescription: "First day, `YYYY-MM-DD`.", Computed: true},
								"period_to":      schema.StringAttribute{MarkdownDescription: "Last day, `YYYY-MM-DD`.", Computed: true},
								"volume_credits": schema.Float64Attribute{MarkdownDescription: "Credits of the rest of the month.", Computed: true},
								"price":          schema.Float64Attribute{MarkdownDescription: "Price of the rest of the month; null when the plan has no price.", Computed: true},
							},
						},
					},
				},
			},
		},
	}
}

func (d *packagePlansDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := clientFrom(req.ProviderData, &resp.Diagnostics); client != nil {
		d.client = client
	}
}

func (d *packagePlansDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	plans, err := d.client.GetLLMPackagePlanList(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read LLM package plans", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, packagePlansStateFrom(plans))...)
}
