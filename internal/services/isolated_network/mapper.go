package isolated_network

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/itglobalcom/vstack-cloud-panel-sdk/entities"
)

// networkModel - shared model for resource and data sources
type networkModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	LocationID    types.String `tfsdk:"location_id"`
	Description   types.String `tfsdk:"description"`
	NetworkPrefix types.String `tfsdk:"network_prefix"`
	Mask          types.Int64  `tfsdk:"mask"`
	ServerIDs     types.List   `tfsdk:"server_ids"`
	GatewayIDs    types.List   `tfsdk:"gateway_ids"`
	Created       types.String `tfsdk:"created"`
	Tags          types.Set    `tfsdk:"tags"`
}

// mapNetworkToModel - shared function to convert from SDK to Terraform model
func mapNetworkToModel(network *entities.Network) networkModel {
	model := networkModel{
		ID:            types.StringValue(network.ID),
		Name:          types.StringValue(network.Name),
		LocationID:    types.StringValue(network.LocationID),
		Description:   types.StringValue(network.Description),
		NetworkPrefix: types.StringValue(network.NetworkPrefix),
		Mask:          types.Int64Value(int64(network.Mask)),
		Created:       types.StringValue(network.Created),
	}

	// Convert ServerIDs
	serverIDValues := make([]attr.Value, len(network.ServerIDs))
	for i, id := range network.ServerIDs {
		serverIDValues[i] = types.StringValue(id)
	}
	model.ServerIDs = types.ListValueMust(types.StringType, serverIDValues)

	// Convert GatewayIDs
	gatewayIDValues := make([]attr.Value, len(network.GatewayIDs))
	for i, id := range network.GatewayIDs {
		gatewayIDValues[i] = types.StringValue(id)
	}
	model.GatewayIDs = types.ListValueMust(types.StringType, gatewayIDValues)

	// Convert Tags
	if len(network.Tags) == 0 {
		model.Tags = types.SetValueMust(types.StringType, []attr.Value{}) // empty set
	} else {
		tagValues := make([]attr.Value, len(network.Tags))
		for i, tag := range network.Tags {
			tagValues[i] = types.StringValue(tag)
		}
		model.Tags = types.SetValueMust(types.StringType, tagValues)
	}
	return model
}
