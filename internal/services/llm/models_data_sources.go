package llm

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/itglobalcom/vstack-cloud-panel-sdk"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

var (
	_ datasource.DataSourceWithConfigure = &modelsDataSource{}
	_ datasource.DataSourceWithConfigure = &modelDataSource{}
)

// modelFields is what the catalog list and a single model share.
type modelFields struct {
	ID                  types.Int64  `tfsdk:"id"`
	TechTitle           types.String `tfsdk:"tech_title"`
	Name                types.String `tfsdk:"name"`
	Description         types.String `tfsdk:"description"`
	Vendor              types.String `tfsdk:"vendor"`
	Kind                types.String `tfsdk:"kind"`
	Contour             types.String `tfsdk:"contour"`
	StorageCountries    types.List   `tfsdk:"storage_countries"`
	IsPIIProtected      types.Bool   `tfsdk:"is_pii_protected"`
	ContextSize         types.Int64  `tfsdk:"context_size"`
	UnitKind            types.String `tfsdk:"unit_kind"`
	Endpoint            types.String `tfsdk:"endpoint"`
	IsAvailableToGroups types.Bool   `tfsdk:"is_available_to_groups"`
}

func modelFieldsFrom(m entities.LLMModel) modelFields {
	description := types.StringNull()
	if m.Description != "" {
		description = types.StringValue(m.Description)
	}
	return modelFields{
		ID:                  types.Int64Value(int64(m.ID)),
		TechTitle:           types.StringValue(m.TechTitle),
		Name:                types.StringValue(m.Name),
		Description:         description,
		Vendor:              types.StringValue(m.Vendor),
		Kind:                types.StringValue(string(m.Kind)),
		Contour:             types.StringValue(string(m.Contour)),
		StorageCountries:    stringList(m.StorageCountries),
		IsPIIProtected:      types.BoolValue(m.IsPIIProtected),
		ContextSize:         types.Int64Value(int64(m.ContextSize)),
		UnitKind:            types.StringValue(string(m.UnitKind)),
		Endpoint:            types.StringValue(m.Endpoint),
		IsAvailableToGroups: types.BoolValue(m.IsAvailableToGroups),
	}
}

// modelFieldAttributes returns the schema of modelFields; id is computed.
func modelFieldAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.Int64Attribute{
			MarkdownDescription: "Model ID, for `model_ids` of `vcp_llm_group`.",
			Computed:            true,
		},
		"tech_title": schema.StringAttribute{
			MarkdownDescription: "Model name the inference calls take in the `model` field.",
			Computed:            true,
		},
		"name": schema.StringAttribute{
			MarkdownDescription: "Display name, in English.",
			Computed:            true,
		},
		"description": schema.StringAttribute{
			MarkdownDescription: "Description, in English.",
			Computed:            true,
		},
		"vendor": schema.StringAttribute{
			MarkdownDescription: "Model vendor.",
			Computed:            true,
		},
		"kind": schema.StringAttribute{
			MarkdownDescription: "Model kind: `text`, `rag`, `speech`, `image` or `video`. New kinds may be added over time.",
			Computed:            true,
		},
		"contour": schema.StringAttribute{
			MarkdownDescription: "`local` for a model served in the local contour, `external` for an external provider.",
			Computed:            true,
		},
		"storage_countries": schema.ListAttribute{
			MarkdownDescription: "Countries the data of the calls is stored in.",
			ElementType:         types.StringType,
			Computed:            true,
		},
		"is_pii_protected": schema.BoolAttribute{
			MarkdownDescription: "Whether personal data in the calls is protected.",
			Computed:            true,
		},
		"context_size": schema.Int64Attribute{
			MarkdownDescription: "Context size, in tokens.",
			Computed:            true,
		},
		"unit_kind": schema.StringAttribute{
			MarkdownDescription: "Unit the model is billed in: `tokens`, `units`, `bytes` or `none`.",
			Computed:            true,
		},
		"endpoint": schema.StringAttribute{
			MarkdownDescription: "Inference address of the model.",
			Computed:            true,
		},
		"is_available_to_groups": schema.BoolAttribute{
			MarkdownDescription: "Whether the model can be granted to a group.",
			Computed:            true,
		},
	}
}

// ----------------------------------------------------------------------
// vcp_llm_models
// ----------------------------------------------------------------------

// NewModelsDataSource returns the vcp_llm_models data source.
func NewModelsDataSource() datasource.DataSource {
	return &modelsDataSource{}
}

type modelsDataSource struct {
	client *sdk.CloudClient
}

type modelsModel struct {
	Models            []modelFields `tfsdk:"models"`
	PricesCollectedAt types.String  `tfsdk:"prices_collected_at"`
}

func (d *modelsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_llm_models"
}

func (d *modelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the LLM model catalog of the project. The list is empty until the project " +
			"is connected to the LLM service (`vcp_llm_connection`). Prices of a model are in `vcp_llm_model`.",
		Attributes: map[string]schema.Attribute{
			"models": schema.ListNestedAttribute{
				MarkdownDescription: "Catalog models.",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: modelFieldAttributes()},
			},
			"prices_collected_at": schema.StringAttribute{
				MarkdownDescription: "When the prices were last collected from the providers, RFC 3339.",
				Computed:            true,
			},
		},
	}
}

func (d *modelsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := clientFrom(req.ProviderData, &resp.Diagnostics); client != nil {
		d.client = client
	}
}

func (d *modelsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	catalog, err := d.client.GetLLMModelList(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read LLM models", err.Error())
		return
	}

	data := modelsModel{
		Models:            make([]modelFields, len(catalog.Models)),
		PricesCollectedAt: optString(catalog.PricesCollectedAt),
	}
	for i, m := range catalog.Models {
		data.Models[i] = modelFieldsFrom(m)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// ----------------------------------------------------------------------
// vcp_llm_model
// ----------------------------------------------------------------------

// NewModelDataSource returns the vcp_llm_model data source.
func NewModelDataSource() datasource.DataSource {
	return &modelDataSource{}
}

type modelDataSource struct {
	client *sdk.CloudClient
}

type modelModel struct {
	modelFields
	DefaultProvider   types.String         `tfsdk:"default_provider"`
	SupportsStreaming types.Bool           `tfsdk:"supports_streaming"`
	SupportsTools     types.Bool           `tfsdk:"supports_tools"`
	CreditPrice       *creditPriceModel    `tfsdk:"credit_price"`
	Prices            []modelPriceModel    `tfsdk:"prices"`
	Providers         []modelProviderModel `tfsdk:"providers"`
}

type modelPriceModel struct {
	Unit    types.String  `tfsdk:"unit"`
	Variant types.String  `tfsdk:"variant"`
	Credits types.Float64 `tfsdk:"credits"`
	Money   types.Float64 `tfsdk:"money"`
}

type modelProviderModel struct {
	Slug                      types.String      `tfsdk:"slug"`
	Name                      types.String      `tfsdk:"name"`
	Variant                   types.String      `tfsdk:"variant"`
	ContextLength             types.Int64       `tfsdk:"context_length"`
	MaxCompletionTokens       types.Int64       `tfsdk:"max_completion_tokens"`
	Discount                  types.Float64     `tfsdk:"discount"`
	IsLive                    types.Bool        `tfsdk:"is_live"`
	SupportsTools             types.Bool        `tfsdk:"supports_tools"`
	SupportsReasoning         types.Bool        `tfsdk:"supports_reasoning"`
	SupportsStructuredOutputs types.Bool        `tfsdk:"supports_structured_outputs"`
	SupportsWebSearch         types.Bool        `tfsdk:"supports_web_search"`
	Prices                    []modelPriceModel `tfsdk:"prices"`
}

type creditPriceModel struct {
	Basis          types.String  `tfsdk:"basis"`
	PricePerCredit types.Float64 `tfsdk:"price_per_credit"`
	Currency       types.String  `tfsdk:"currency"`
	TaxLabel       types.String  `tfsdk:"tax_label"`
	PackageName    types.String  `tfsdk:"package_name"`
	ValidThrough   types.String  `tfsdk:"valid_through"`
}

func modelPricesFrom(prices []entities.LLMModelPrice) []modelPriceModel {
	out := make([]modelPriceModel, len(prices))
	for i, p := range prices {
		out[i] = modelPriceModel{
			Unit:    types.StringValue(string(p.Unit)),
			Variant: optString(p.Variant),
			Credits: types.Float64Value(p.Credits),
			Money:   optFloat(p.Money),
		}
	}
	return out
}

func creditPriceFrom(p *entities.LLMCreditPrice) *creditPriceModel {
	if p == nil {
		return nil
	}
	return &creditPriceModel{
		Basis:          types.StringValue(string(p.Basis)),
		PricePerCredit: types.Float64Value(p.PricePerCredit),
		Currency:       types.StringValue(p.Currency),
		TaxLabel:       optTaxLabel(p.TaxLabel),
		PackageName:    optString(p.PackageName),
		ValidThrough:   optString(p.ValidThrough),
	}
}

func modelStateFrom(m *entities.LLMModelDetails) *modelModel {
	providers := make([]modelProviderModel, len(m.Providers))
	for i, p := range m.Providers {
		providers[i] = modelProviderModel{
			Slug:                      types.StringValue(p.Slug),
			Name:                      types.StringValue(p.Name),
			Variant:                   optString(p.Variant),
			ContextLength:             optInt(p.ContextLength),
			MaxCompletionTokens:       optInt(p.MaxCompletionTokens),
			Discount:                  types.Float64Value(p.Discount),
			IsLive:                    types.BoolValue(p.IsLive),
			SupportsTools:             types.BoolValue(p.SupportsTools),
			SupportsReasoning:         types.BoolValue(p.SupportsReasoning),
			SupportsStructuredOutputs: types.BoolValue(p.SupportsStructuredOutputs),
			SupportsWebSearch:         types.BoolValue(p.SupportsWebSearch),
			Prices:                    modelPricesFrom(p.Prices),
		}
	}
	return &modelModel{
		modelFields:       modelFieldsFrom(m.LLMModel),
		DefaultProvider:   types.StringValue(m.DefaultProvider),
		SupportsStreaming: optBool(m.SupportsStreaming),
		SupportsTools:     optBool(m.SupportsTools),
		CreditPrice:       creditPriceFrom(m.CreditPrice),
		Prices:            modelPricesFrom(m.Prices),
		Providers:         providers,
	}
}

func modelPriceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"unit": schema.StringAttribute{
			MarkdownDescription: "Unit the price is quoted for, e.g. `input_tokens`, `output_tokens`, " +
				"`request`, `audio_minute`. New units may be added over time.",
			Computed: true,
		},
		"variant": schema.StringAttribute{
			MarkdownDescription: "Price variant, where the model has several.",
			Computed:            true,
		},
		"credits": schema.Float64Attribute{
			MarkdownDescription: "Price in credits.",
			Computed:            true,
		},
		"money": schema.Float64Attribute{
			MarkdownDescription: "Price in the currency of `credit_price`; null when usage is in credits only or the model is not priced.",
			Computed:            true,
		},
	}
}

// creditPriceAttribute is the schema of creditPriceModel.
func creditPriceAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: description,
		Computed:            true,
		Attributes: map[string]schema.Attribute{
			"basis": schema.StringAttribute{
				MarkdownDescription: "Where the price comes from: `package`, `project_discount`, `individual` " +
					"or `pay_as_you_go`.",
				Computed: true,
			},
			"price_per_credit": schema.Float64Attribute{
				MarkdownDescription: "Price of one credit.",
				Computed:            true,
			},
			"currency": schema.StringAttribute{
				MarkdownDescription: "Currency code, e.g. `RUB`.",
				Computed:            true,
			},
			"tax_label": schema.StringAttribute{
				MarkdownDescription: "Tax caption of the price: `nds_excluded` or `vat_excluded`.",
				Computed:            true,
			},
			"package_name": schema.StringAttribute{
				MarkdownDescription: "Package the price comes from, for the `package` basis.",
				Computed:            true,
			},
			"valid_through": schema.StringAttribute{
				MarkdownDescription: "Last day the price holds, `YYYY-MM-DD`.",
				Computed:            true,
			},
		},
	}
}

func (d *modelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_llm_model"
}

func (d *modelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := modelFieldAttributes()
	attrs["id"] = schema.Int64Attribute{
		MarkdownDescription: "Model ID, from `vcp_llm_models`.",
		Required:            true,
		Validators:          []validator.Int64{int64validator.AtLeast(1)},
	}
	attrs["default_provider"] = schema.StringAttribute{
		MarkdownDescription: "Slug of the provider the calls go to by default.",
		Computed:            true,
	}
	attrs["supports_streaming"] = schema.BoolAttribute{
		MarkdownDescription: "Whether the model streams its answers.",
		Computed:            true,
	}
	attrs["supports_tools"] = schema.BoolAttribute{
		MarkdownDescription: "Whether the model calls tools.",
		Computed:            true,
	}
	attrs["credit_price"] = creditPriceAttribute("Price of one credit for the project; null when usage is in credits only.")
	attrs["prices"] = schema.ListNestedAttribute{
		MarkdownDescription: "Prices of the model by unit and variant.",
		Computed:            true,
		NestedObject:        schema.NestedAttributeObject{Attributes: modelPriceAttributes()},
	}
	providerAttrs := map[string]schema.Attribute{
		"slug":                        schema.StringAttribute{MarkdownDescription: "Provider slug.", Computed: true},
		"name":                        schema.StringAttribute{MarkdownDescription: "Provider name.", Computed: true},
		"variant":                     schema.StringAttribute{MarkdownDescription: "Model variant the provider serves.", Computed: true},
		"context_length":              schema.Int64Attribute{MarkdownDescription: "Context length at the provider, in tokens.", Computed: true},
		"max_completion_tokens":       schema.Int64Attribute{MarkdownDescription: "Longest answer at the provider, in tokens.", Computed: true},
		"discount":                    schema.Float64Attribute{MarkdownDescription: "Discount of the provider, percent.", Computed: true},
		"is_live":                     schema.BoolAttribute{MarkdownDescription: "Whether the provider serves calls now.", Computed: true},
		"supports_tools":              schema.BoolAttribute{MarkdownDescription: "Whether tool calls are served.", Computed: true},
		"supports_reasoning":          schema.BoolAttribute{MarkdownDescription: "Whether reasoning is served.", Computed: true},
		"supports_structured_outputs": schema.BoolAttribute{MarkdownDescription: "Whether structured outputs are served.", Computed: true},
		"supports_web_search":         schema.BoolAttribute{MarkdownDescription: "Whether web search is served.", Computed: true},
		"prices": schema.ListNestedAttribute{
			MarkdownDescription: "Prices at the provider.",
			Computed:            true,
			NestedObject:        schema.NestedAttributeObject{Attributes: modelPriceAttributes()},
		},
	}
	attrs["providers"] = schema.ListNestedAttribute{
		MarkdownDescription: "Providers serving the model.",
		Computed:            true,
		NestedObject:        schema.NestedAttributeObject{Attributes: providerAttrs},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a model of the LLM catalog with its prices, providers and capabilities.",
		Attributes:          attrs,
	}
}

func (d *modelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := clientFrom(req.ProviderData, &resp.Diagnostics); client != nil {
		d.client = client
	}
}

func (d *modelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config modelModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	model, err := d.client.GetLLMModel(ctx, entities.LLMModelID(config.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Unable to read LLM model", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, modelStateFrom(model))...)
}
