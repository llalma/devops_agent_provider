package provider

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/llalma/devops_agent_provider/internal/resources"
)

// Ensure the implementation satisfies the expected interfaces
var _ provider.Provider = &DevOpsAgentProvider{}

type DevOpsAgentProvider struct {
	version string
}

type DevOpsAgentProviderModel struct {
	Region  types.String `tfsdk:"region"`
	Profile types.String `tfsdk:"profile"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &DevOpsAgentProvider{
			version: version,
		}
	}
}

func (p *DevOpsAgentProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "devops_agent"
	resp.Version = p.version
}

func (p *DevOpsAgentProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Provider for managing custom DevOps Agent resources via AWS.",
		Attributes: map[string]schema.Attribute{
			"region": schema.StringAttribute{
				MarkdownDescription: "AWS Region. If not set, defaults to the AWS environment variables.",
				Optional:            true,
			},
			"profile": schema.StringAttribute{
				MarkdownDescription: "AWS CLI Profile name to use.",
				Optional:            true,
			},
		},
	}
}

func (p *DevOpsAgentProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data DevOpsAgentProviderModel
	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Gather optional overrides
	var loadOpts []func(*config.LoadOptions) error
	if !data.Region.IsNull() && !data.Region.IsUnknown() {
		loadOpts = append(loadOpts, config.WithRegion(data.Region.ValueString()))
	}
	if !data.Profile.IsNull() && !data.Profile.IsUnknown() {
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(data.Profile.ValueString()))
	}

	// Load AWS default configuration chain (~/.aws/credentials, env vars, etc.)
	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		resp.Diagnostics.AddError(
			"AWS Configuration Error",
			"Failed to load default AWS config. Ensure your credentials are set up. Error: "+err.Error(),
		)
		return
	}

	// Configure the region
	region := cfg.Region
	if !data.Region.IsNull() && !data.Region.IsUnknown() {
		region = data.Region.ValueString()
	}

	// Create a devops client
	client := devopsagent.NewFromConfig(cfg, func(o *devopsagent.Options) {
		o.Region = region
	})

	// Pass this client down to your resource implementations
	resp.ResourceData = client
}

// DataSources defines the data sources implemented in the provider.
func (p *DevOpsAgentProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

// Resources defines the resources implemented in the provider.
func (p *DevOpsAgentProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource { return &resources.SkillResource{} },
		func() resource.Resource { return &resources.InstructionsResource{} },
	}
}
