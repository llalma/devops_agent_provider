package resources

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/types"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/document"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	tfTypes "github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var _ resource.Resource = &SkillResource{}
var _ resource.ResourceWithConfigure = &SkillResource{}

type SkillResource struct {
	client *devopsagent.Client
}

// Define the inputs
type SkillResourceModel struct {
	ID           tfTypes.String `tfsdk:"id"`
	Name         tfTypes.String `tfsdk:"name"`
	Description  tfTypes.String `tfsdk:"description"`
	AgentTypes 	 tfTypes.Set    `tfsdk:"agent_types"`
	Content 		 tfTypes.String `tfsdk:"content"`
	AgentSpaceID tfTypes.String   `tfsdk:"agentspace_id"`
}

// Define the resource name
func (r *SkillResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill"
}

func (r *SkillResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	
	c, ok := req.ProviderData.(*devopsagent.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *SkillResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a DevOps agent skill using the official AWS DevOps Agent SDK.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Unique identifier for the skill asset.",
				Computed:            true,

				// Will stop plan from thinking the ID will be replaced
				PlanModifiers: []planmodifier.String{
						stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the skill.",
				Required:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Description of the skill.",
				Required:            true,
			},
			"agent_types": schema.SetAttribute{
				MarkdownDescription: "Which Agent the skill applies to",
				Optional:            true,
				Computed: 					 true,		
				ElementType:         tfTypes.StringType,
				Default: setdefault.StaticValue(
					tfTypes.SetValueMust(
							tfTypes.StringType,
							[]attr.Value{
									tfTypes.StringValue("GENERIC"),
							},
					),
				),
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(
						stringvalidator.OneOf("GENERIC", "CHAT", "INCIDENT_TRIAGE", "INCIDENT_RCA", "INCIDENT_MITIGATION", "PREVENTION", "CHANGE_REVIEW", "CHANGE_RELEASE", "QUALITY_ASSURANCE_TESTING", "RELEASE_SHEPHERD", "RELEASE_READINESS_REVIEW", "RELEASE_TESTING", "SYSTEM_LEARNING", "INCIDENT_UI", "GRADER",
						),
					),
				},
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

func (m *SkillResourceModel) ContentPayload() *types.AssetContentMemberFile {
    return &types.AssetContentMemberFile{
        Value: types.AssetFileContent{
            Path: aws.String("SKILL.md"),
            Body: &types.AssetFileBodyMemberText{
                Value: m.Content.ValueString(),
            },
        },
    }
}

// Build SDK Metadata document
func (m *SkillResourceModel) MetadataPayload(ctx context.Context) document.Interface {
	var agentTypes []string
	m.AgentTypes.ElementsAs(ctx, &agentTypes, false)

	return document.NewLazyDocument(map[string]interface{}{
			"name":        m.Name.ValueString(),
			"description": m.Description.ValueString(),
			"agent_types": agentTypes,
	})
}

func (r *SkillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SkillResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create the input for the SDK
	input := &devopsagent.CreateAssetInput{
        AgentSpaceId: aws.String(plan.AgentSpaceID.ValueString()),
        Content:      plan.ContentPayload(),
        Metadata:     plan.MetadataPayload(ctx),
    }

	// Execute via SDK
	output, err := r.client.CreateAsset(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError("API Request Failed", err.Error())
		return
	}

	// Fetch the ID of the created skill
	if output.Asset != nil && output.Asset.AssetId != nil {
		plan.ID = tfTypes.StringValue(*output.Asset.AssetId)
	} else {
		resp.Diagnostics.AddError("API Error", "Could not extract assetId from SDK response")
		return
	}
	
	// Update the state file
	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

func (r *SkillResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {}

func (r *SkillResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state SkillResourceModel

	// Get the plan and state info
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	diags = req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
			return
	}

	// Create the input for the SDK
	input := &devopsagent.UpdateAssetInput{
			AgentSpaceId: aws.String(plan.AgentSpaceID.ValueString()),
			AssetId: aws.String(state.ID.ValueString()),
			Content:      plan.ContentPayload(),
			Metadata:     plan.MetadataPayload(ctx),
	}

	// Execute via SDK
	_, err := r.client.UpdateAsset(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError("Error Updateing Skill", err.Error())
		return
	}
	
	// Update the state file
	plan.ID = state.ID
	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

func (r *SkillResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {}
