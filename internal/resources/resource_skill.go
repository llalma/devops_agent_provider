package resources

import (
	"context"
	"strings"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/types"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/document"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	tfTypes "github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/llalma/devops_agent_provider/internal/client"
)

var _ resource.Resource = &SkillResource{}
var _ resource.ResourceWithConfigure = &SkillResource{}

type SkillResource struct {
	client *client.Client
}

type SkillResourceModel struct {
	ID           tfTypes.String `tfsdk:"id"`
	Name         tfTypes.String `tfsdk:"name"`
	Description  tfTypes.String `tfsdk:"description"`
	Content 		 tfTypes.String `tfsdk:"content"`
	AgentSpaceID tfTypes.String `tfsdk:"agentspace_id"`
}

func (r *SkillResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill"
}

func (r *SkillResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

type SkillMetadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	AgentTypes  []string `json:"agent_types"`
}

func (r *SkillResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a DevOps agent skill using the official AWS DevOps Agent SDK.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Unique identifier for the skill asset.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the skill.",
				Required:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Description of the skill.",
				Required:            true,
			},
			"content": schema.StringAttribute{
				MarkdownDescription: "Content of the skill",
				Required: true,
			},
			"agentspace_id": schema.StringAttribute{
				MarkdownDescription: "The agent_space_id of the DevOps agent space.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *SkillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SkillResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentSpaceID := strings.Trim(plan.AgentSpaceID.ValueString(), "\"")

	// Initialize the official DevOps Agent SDK client using provider config
	devopsClient := devopsagent.NewFromConfig(r.client.AwsConfig, func(o *devopsagent.Options) {
		if r.client.Region != "" {
			o.Region = r.client.Region
		}
	})
	
	skillContent := strings.Trim(plan.Content.ValueString(), "\"")
	skillName := strings.Trim(plan.Name.ValueString(), "\"")
	skillDescription := strings.Trim(plan.Description.ValueString(), "\"")

	// Construct the input matching the CreateAsset API specification
	input := &devopsagent.CreateAssetInput{
		AgentSpaceId: aws.String(agentSpaceID),
		AssetType:    aws.String("skill"),
		Content: &types.AssetContentMemberFile{
			Value: types.AssetFileContent{
				Path: aws.String("SKILL.md"),
				Body: &types.AssetFileBodyMemberText{
					Value: skillContent,
				},
			},
		},
		Metadata: document.NewLazyDocument(map[string]interface{}{
			"name":        skillName,
			"description": skillDescription,
			"agent_types": []string{"GENERIC"},
		}),
	}

	// Execute via SDK
	output, err := devopsClient.CreateAsset(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError("API Request Failed", err.Error())
		return
	}

	if output.Asset != nil && output.Asset.AssetId != nil {
		plan.ID = tfTypes.StringValue(*output.Asset.AssetId)
	} else {
		resp.Diagnostics.AddError("API Error", "Could not extract assetId from SDK response")
		return
	}

	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

func (r *SkillResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {}
func (r *SkillResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {}
func (r *SkillResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {}
