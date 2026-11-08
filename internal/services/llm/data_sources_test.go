package llm

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The catalog and package data sources read on wire literals. The API leaves
// out what it has no value for; each such field has to come through as null —
// not as an empty string or zero, which reads as a real value, and not as a
// state the schema refuses. A second, full answer checks the values land on
// the right attributes.

func TestModelsDataSourceRead(t *testing.T) {
	api := newFakeAPI(t)
	api.setRaw("llm/models", `{
		"models": [
			{"id": 7, "tech_title": "gpt-x", "name": "GPT X", "description": "Chat model", "vendor": "acme",
			 "kind": "text", "contour": "external", "storage_countries": ["RU", "KZ"], "is_pii_protected": true,
			 "context_size": 128000, "unit_kind": "tokens", "endpoint": "https://llm.example/v1",
			 "is_available_to_groups": true},
			{"id": 8, "tech_title": "whisper", "name": "Whisper", "vendor": "acme", "kind": "speech",
			 "contour": "local", "is_pii_protected": false, "context_size": 0, "unit_kind": "none",
			 "endpoint": "https://llm.example/v1", "is_available_to_groups": false}
		]
	}`)

	var out modelsModel
	readDataSource(t, api, NewModelsDataSource(), nil, &out)

	if len(out.Models) != 2 {
		t.Fatalf("read %d models, want 2", len(out.Models))
	}
	full, bare := out.Models[0], out.Models[1]
	if full.ID.ValueInt64() != 7 || full.TechTitle.ValueString() != "gpt-x" || full.Kind.ValueString() != "text" ||
		full.Contour.ValueString() != "external" || full.ContextSize.ValueInt64() != 128000 ||
		!full.IsPIIProtected.ValueBool() || !full.IsAvailableToGroups.ValueBool() ||
		full.Description.ValueString() != "Chat model" || full.Endpoint.ValueString() != "https://llm.example/v1" {
		t.Errorf("model 7 = %+v, want the catalog values", full)
	}
	if len(full.StorageCountries.Elements()) != 2 {
		t.Errorf("storage_countries = %v, want [RU KZ]", full.StorageCountries)
	}
	if !bare.Description.IsNull() {
		t.Errorf("a model without a description reads description = %v, want null", bare.Description)
	}
	if bare.IsAvailableToGroups.ValueBool() || bare.Kind.ValueString() != "speech" {
		t.Errorf("model 8 = %+v, want speech, not available to groups", bare)
	}
	if !out.PricesCollectedAt.IsNull() {
		t.Errorf("prices_collected_at = %v, want null when the API leaves it out", out.PricesCollectedAt)
	}
}

func TestModelDataSourceRead(t *testing.T) {
	api := newFakeAPI(t)
	api.setRaw("llm/models/7", `{"model": {
		"id": 7, "tech_title": "gpt-x", "name": "GPT X", "vendor": "acme", "kind": "text", "contour": "external",
		"is_pii_protected": false, "context_size": 8192, "unit_kind": "tokens", "endpoint": "https://llm.example/v1",
		"is_available_to_groups": true, "default_provider": "openrouter", "supports_streaming": true,
		"credit_price": {"basis": "package", "price_per_credit": 0.5, "currency": "RUB", "package_name": "S"},
		"prices": [{"unit": "input_tokens", "credits": 1.5, "money": 0.75}, {"unit": "output_tokens", "variant": "batch", "credits": 3}],
		"providers": [{"slug": "openrouter", "name": "OpenRouter", "discount": 10, "is_live": true,
		               "supports_tools": true, "supports_reasoning": false, "supports_structured_outputs": false,
		               "supports_web_search": false, "prices": [{"unit": "input_tokens", "credits": 1}]}]
	}}`)

	config := modelModel{}
	config.ID = types.Int64Value(7)
	config.StorageCountries = types.ListNull(types.StringType)
	var out modelModel
	readDataSource(t, api, NewModelDataSource(), &config, &out)

	if out.ID.ValueInt64() != 7 || out.DefaultProvider.ValueString() != "openrouter" || !out.SupportsStreaming.ValueBool() {
		t.Errorf("model = %+v, want 7 by openrouter, streaming", out)
	}
	if !out.SupportsTools.IsNull() {
		t.Errorf("supports_tools = %v, want null when the API leaves it out", out.SupportsTools)
	}
	if out.CreditPrice == nil || out.CreditPrice.PricePerCredit.ValueFloat64() != 0.5 ||
		out.CreditPrice.PackageName.ValueString() != "S" || !out.CreditPrice.TaxLabel.IsNull() ||
		!out.CreditPrice.ValidThrough.IsNull() {
		t.Errorf("credit_price = %+v, want 0.5 RUB of package S, no tax label or end date", out.CreditPrice)
	}
	if len(out.Prices) != 2 {
		t.Fatalf("read %d prices, want 2", len(out.Prices))
	}
	if out.Prices[0].Money.ValueFloat64() != 0.75 || !out.Prices[0].Variant.IsNull() {
		t.Errorf("prices[0] = %+v, want money 0.75 and no variant", out.Prices[0])
	}
	if !out.Prices[1].Money.IsNull() || out.Prices[1].Variant.ValueString() != "batch" || out.Prices[1].Credits.ValueFloat64() != 3 {
		t.Errorf("prices[1] = %+v, want 3 credits for batch, no money", out.Prices[1])
	}
	if len(out.Providers) != 1 || out.Providers[0].Discount.ValueFloat64() != 10 ||
		!out.Providers[0].ContextLength.IsNull() || len(out.Providers[0].Prices) != 1 {
		t.Errorf("providers = %+v, want openrouter with discount 10, one price, no context length", out.Providers)
	}
}

func TestModelDataSourceReadCreditsOnly(t *testing.T) {
	api := newFakeAPI(t)
	api.setRaw("llm/models/7", `{"model": {
		"id": 7, "tech_title": "gpt-x", "name": "GPT X", "vendor": "acme", "kind": "text", "contour": "external",
		"is_pii_protected": false, "context_size": 8192, "unit_kind": "tokens", "endpoint": "https://llm.example/v1",
		"is_available_to_groups": true, "prices": [{"unit": "input_tokens", "credits": 1.5}]
	}}`)

	config := modelModel{}
	config.ID = types.Int64Value(7)
	config.StorageCountries = types.ListNull(types.StringType)
	var out modelModel
	readDataSource(t, api, NewModelDataSource(), &config, &out)

	if out.CreditPrice != nil {
		t.Errorf("credit_price = %+v, want null in a project without money", out.CreditPrice)
	}
	if len(out.Prices) != 1 || !out.Prices[0].Money.IsNull() || out.Prices[0].Credits.ValueFloat64() != 1.5 {
		t.Errorf("prices = %+v, want 1.5 credits and no money", out.Prices)
	}
}

func TestPackageDataSourceRead(t *testing.T) {
	t.Run("no package, no stop", func(t *testing.T) {
		api := newFakeAPI(t)
		api.setRaw("llm/package", `{"package": {
			"usage": {"period_from": "2026-11-01", "period_to": "2026-12-01", "total_credits": 12.5,
			          "in_package_credits": 0, "over_package_credits": 12.5},
			"currency": "RUB", "packages_disabled": false, "exhausted_package_mode": "continue_pay_as_you_go"
		}}`)

		var out currentPackageModel
		readDataSource(t, api, NewPackageDataSource(), nil, &out)

		if out.Package != nil || out.Stop != nil {
			t.Errorf("package = %+v, stop = %+v, want both null", out.Package, out.Stop)
		}
		if !out.PricePerCredit.IsNull() || !out.TaxLabel.IsNull() || !out.PayAsYouGoLimitCredits.IsNull() ||
			!out.Usage.PackageVolumeCredits.IsNull() {
			t.Errorf("absent numbers read as values: %+v", out)
		}
		if out.Usage.TotalCredits.ValueFloat64() != 12.5 || out.ExhaustedPackageMode.ValueString() != "continue_pay_as_you_go" {
			t.Errorf("usage/mode = %+v / %v", out.Usage, out.ExhaustedPackageMode)
		}
	})

	t.Run("package and stop", func(t *testing.T) {
		api := newFakeAPI(t)
		api.setRaw("llm/package", `{"package": {
			"package": {"id": 3, "tech_title": "S", "volume_credits": 1000, "discount_percent": 5, "price": 950,
			            "period_from": "2026-11-01", "period_to": "2026-12-01", "auto_renew": true, "state": "active"},
			"usage": {"period_from": "2026-11-01", "period_to": "2026-12-01", "total_credits": 1000,
			          "package_volume_credits": 1000, "in_package_credits": 1000, "over_package_credits": 0},
			"price_per_credit": 0.95, "currency": "RUB", "tax_label": "nds_excluded", "packages_disabled": false,
			"exhausted_package_mode": "stop",
			"stop": {"stopped_at": "2026-11-20T10:00:00Z", "reason": "package_exhausted", "state": "stopped"}
		}}`)

		var out currentPackageModel
		readDataSource(t, api, NewPackageDataSource(), nil, &out)

		if out.Package == nil || out.Package.ID.ValueInt64() != 3 || !out.Package.AutoRenew.ValueBool() ||
			!out.Package.FullMonthPrice.IsNull() || out.Package.Price.ValueFloat64() != 950 ||
			out.Package.DiscountPercent.ValueFloat64() != 5 {
			t.Errorf("package = %+v, want 3 for 950 at 5%% with auto renew and no full-month price", out.Package)
		}
		if out.Currency.ValueString() != "RUB" {
			t.Errorf("currency = %v, want RUB", out.Currency)
		}
		if out.Stop == nil || out.Stop.Reason.ValueString() != "package_exhausted" {
			t.Errorf("stop = %+v, want package_exhausted", out.Stop)
		}
		if out.TaxLabel.ValueString() != "nds_excluded" || out.PricePerCredit.ValueFloat64() != 0.95 {
			t.Errorf("tax_label/price = %v/%v", out.TaxLabel, out.PricePerCredit)
		}
	})

	// Usage in credits only: the API leaves out every price and the currency
	// (or sends it empty); an empty currency read as a value claims a real one.
	t.Run("credits only, stopped by the project quota", func(t *testing.T) {
		api := newFakeAPI(t)
		api.setRaw("llm/package", `{"package": {
			"package": {"id": 3, "tech_title": "S", "volume_credits": 1000,
			            "period_from": "2026-11-01", "period_to": "2026-12-01", "auto_renew": false, "state": "active"},
			"usage": {"period_from": "2026-11-01", "period_to": "2026-12-01", "total_credits": 1000,
			          "package_volume_credits": 1000, "in_package_credits": 1000, "over_package_credits": 0},
			"currency": "", "packages_disabled": false, "exhausted_package_mode": "stop",
			"stop": {"stopped_at": "2026-11-20T10:00:00Z", "reason": "project_quota_exhausted", "state": "stopped"}
		}}`)

		var out currentPackageModel
		readDataSource(t, api, NewPackageDataSource(), nil, &out)

		if out.Package == nil || out.Package.VolumeCredits.ValueFloat64() != 1000 {
			t.Fatalf("package = %+v, want 1000 credits", out.Package)
		}
		if !out.Package.Price.IsNull() || !out.Package.DiscountPercent.IsNull() {
			t.Errorf("price/discount = %v/%v, want null in a package without a price",
				out.Package.Price, out.Package.DiscountPercent)
		}
		if !out.Currency.IsNull() || !out.PricePerCredit.IsNull() || !out.TaxLabel.IsNull() {
			t.Errorf("currency/price per credit/tax label = %v/%v/%v, want null", out.Currency, out.PricePerCredit, out.TaxLabel)
		}
		if out.Stop == nil || out.Stop.Reason.ValueString() != "project_quota_exhausted" {
			t.Errorf("stop = %+v, want project_quota_exhausted", out.Stop)
		}
	})
}

func TestPackagePlansDataSourceRead(t *testing.T) {
	api := newFakeAPI(t)
	api.setRaw("llm/package-plans", `{"package_plans": [
		{"id": 1, "tech_title": "S", "description": "Small", "volume_credits": 1000, "discount_percent": 5,
		 "price": 950, "currency": "RUB", "tax_label": "nds_excluded",
		 "current_month": {"period_from": "2026-11-07", "period_to": "2026-12-01", "volume_credits": 800, "price": 760}},
		{"id": 2, "tech_title": "M", "volume_credits": 5000, "discount_percent": 10, "price": 4500, "currency": "RUB"},
		{"id": 3, "tech_title": "L", "volume_credits": 9000,
		 "current_month": {"period_from": "2026-11-07", "period_to": "2026-12-01", "volume_credits": 7000}}
	]}`)

	var out packagePlansModel
	readDataSource(t, api, NewPackagePlansDataSource(), nil, &out)

	if len(out.Plans) != 3 {
		t.Fatalf("read %d plans, want 3", len(out.Plans))
	}
	small, medium, credits := out.Plans[0], out.Plans[1], out.Plans[2]
	if small.CurrentMonth == nil || small.CurrentMonth.Price.ValueFloat64() != 760 ||
		small.Description.ValueString() != "Small" || small.TaxLabel.ValueString() != "nds_excluded" {
		t.Errorf("plan S = %+v, want its description, tax label and month terms", small)
	}
	if medium.CurrentMonth != nil || !medium.Description.IsNull() || !medium.TaxLabel.IsNull() {
		t.Errorf("plan M = %+v, want no month terms, description or tax label", medium)
	}
	if medium.Price.ValueFloat64() != 4500 || medium.TechTitle.ValueString() != "M" ||
		medium.DiscountPercent.ValueFloat64() != 10 || medium.Currency.ValueString() != "RUB" {
		t.Errorf("plan M = %+v, want M for 4500 RUB at 10%%", medium)
	}
	if !credits.Price.IsNull() || !credits.DiscountPercent.IsNull() || !credits.Currency.IsNull() ||
		!credits.TaxLabel.IsNull() || credits.VolumeCredits.ValueFloat64() != 9000 {
		t.Errorf("plan L = %+v, want 9000 credits and no price, discount, currency or tax label", credits)
	}
	if credits.CurrentMonth == nil || !credits.CurrentMonth.Price.IsNull() ||
		credits.CurrentMonth.VolumeCredits.ValueFloat64() != 7000 {
		t.Errorf("plan L month = %+v, want 7000 credits and no price", credits.CurrentMonth)
	}
}
