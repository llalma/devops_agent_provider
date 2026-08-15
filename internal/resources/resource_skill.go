package resources

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/document"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	tfTypes "github.com/hashicorp/terraform-plugin-framework/types"
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
	AgentTypes   tfTypes.Set    `tfsdk:"agent_types"`
	ContentFile  tfTypes.String `tfsdk:"content_file"`
	ContentHash  tfTypes.String `tfsdk:"content_hash"`
	AgentSpaceID tfTypes.String `tfsdk:"agentspace_id"`
}

type AssetMetadata struct {
	Name        string   `document:"name"`
	Description *string  `document:"description,omitempty"`
	AgentTypes  []string `document:"agent_types"`
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
				Computed:            true,
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
						stringvalidator.OneOf("GENERIC", "CHAT", "INCIDENT_TRIAGE", "INCIDENT_RCA", "INCIDENT_MITIGATION", "PREVENTION", "CHANGE_REVIEW", "CHANGE_RELEASE", "QUALITY_ASSURANCE_TESTING", "RELEASE_SHEPHERD", "RELEASE_READINESS_REVIEW", "RELEASE_TESTING", "SYSTEM_LEARNING", "INCIDENT_UI", "GRADER"),
					),
				},
			},
			"content_file": schema.StringAttribute{
				MarkdownDescription: "Path to the file containing the skill content.",
				Required:            true,
			},
			"content_hash": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Base64 SHA256 hash of the local content_file. Auto-computed if omitted.",
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

// Create the content payload
func (m *SkillResourceModel) ContentPayload() (*types.AssetContentMemberFile, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Read the file from the user's disk
	contentBytes, err := os.ReadFile(m.ContentFile.ValueString())
	if err != nil {
		diags.AddError(
			"Error Reading Content File",
			"Could not read the file at path: "+m.ContentFile.ValueString()+"\nError: "+err.Error(),
		)
		return nil, diags
	}

	return &types.AssetContentMemberFile{
		Value: types.AssetFileContent{
			Path: aws.String("SKILL.md"),
			Body: &types.AssetFileBodyMemberBytes{
				Value: contentBytes,
			},
		},
	}, diags
}

// Create the metadata payload
func (m *SkillResourceModel) MetadataPayload(ctx context.Context) document.Interface {
	var agentTypes []string
	m.AgentTypes.ElementsAs(ctx, &agentTypes, false)

	return document.NewLazyDocument(AssetMetadata{
		Name:        m.Name.ValueString(),
		Description: m.Description.ValueStringPointer(),
		AgentTypes:  agentTypes,
	})
}

// Runs every plan - Used to generate file hash from path instead of user passing it in
func (r *SkillResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Skip if resource is being destroyed
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan SkillResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bytes, err := os.ReadFile(plan.ContentFile.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("File Read Error", "Could not read content_file for hashing: "+err.Error())
		return
	}

	// Calculate sha256 base64 hash
	hash := sha256.Sum256(bytes)
	hashStr := base64.StdEncoding.EncodeToString(hash[:])

	// Update the planned hash value
	plan.ContentHash = tfTypes.StringValue(hashStr)
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *SkillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SkillResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get the file content
	contentPayload, diags := plan.ContentPayload()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create the input for the SDK
	input := &devopsagent.CreateAssetInput{
		AgentSpaceId: aws.String(plan.AgentSpaceID.ValueString()),
		AssetType:    aws.String("skill"),
		Content:      contentPayload,
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

func (r *SkillResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SkillResourceModel

	// Get the state info
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Build the input
	input := &devopsagent.GetAssetInput{
		AgentSpaceId: aws.String(state.AgentSpaceID.ValueString()),
		AssetId:      aws.String(state.ID.ValueString()),
	}

	// Fetch from aws
	output, err := r.client.GetAsset(ctx, input)
	if err != nil {
		var notFoundErr *types.ResourceNotFoundException

		// Check if error is not found. If so remove from state
		if errors.As(err, notFoundErr) {
			fmt.Println("Asset not found!")
			resp.State.RemoveResource(ctx)
			return
		}
	}

	// Extract metadata
	var metadata AssetMetadata
	err = output.Asset.Metadata.UnmarshalSmithyDocument(&metadata)
	if err != nil {
		resp.Diagnostics.AddError("Metadata Error", err.Error())
		return
	}

	// Extract metadata
	state.Name = tfTypes.StringValue(metadata.Name)
	state.Description = tfTypes.StringPointerValue(metadata.Description)
	// state.AgentTypes = tfTypes.StringValue(metadata.AgentTypes)
	// Create AgentTypes
	agentTypeValues := make([]attr.Value, len(metadata.AgentTypes))
	for i, at := range metadata.AgentTypes {
		agentTypeValues[i] = tfTypes.StringValue(at)
	}
	setVal, diags := tfTypes.SetValue(tfTypes.StringType, agentTypeValues)
	resp.Diagnostics.Append(diags...)
	state.AgentTypes = setVal

	file_input := &devopsagent.GetAssetFileInput{
		AgentSpaceId: aws.String(state.AgentSpaceID.ValueString()),
		AssetId:      aws.String(state.ID.ValueString()),
		Path:         aws.String("SKILL.md"),
	}

	// Fetch the content
	output_file, _ := r.client.GetAssetFile(ctx, file_input)
	if output_file.File != nil && output_file.File.Content != nil {
		var fileBytes []byte

		// 1. Unwrap the interface using a type switch
		switch content := output_file.File.Content.(type) {

		// If it is a string member (common for config files/markdown)
		case *types.AssetFileBodyMemberText:
			fileBytes = []byte(content.Value)

		// If it is a byte member (common for binaries/zips)
		case *types.AssetFileBodyMemberBytes:
			fileBytes = content.Value

		default:
			resp.Diagnostics.AddWarning(
				"Unknown File Content Type",
				"AWS returned a file type Terraform cannot hash.",
			)
		}

		// 2. Hash and Base64 Encode
		if len(fileBytes) > 0 {
			contentStr := string(fileBytes)

			// Some reason if editing via the UI it adds in name and description into the file. Causing the hash to differ. If its present only get the actual file content
			if strings.HasPrefix(strings.TrimSpace(contentStr), "---") {
				parts := strings.SplitN(contentStr, "---", 3)
				if len(parts) == 3 {
					// Remove residual leading newlines after closing delimiter
					bodyStr := strings.TrimLeft(parts[2], "\r\n")
					fileBytes = []byte(bodyStr)
				}
			}

			// 2. Hash the cleaned body to match local disk content
			hash := sha256.Sum256(fileBytes)
			hashString := base64.StdEncoding.EncodeToString(hash[:])

			// 3. Save to state for drift detection
			state.ContentHash = tfTypes.StringValue(hashString)
		} else {
			state.ContentHash = tfTypes.StringNull()
		}
	}

	// Update the state
	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

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

	// Get the file content
	contentPayload, diags := plan.ContentPayload()
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create the input for the SDK
	input := &devopsagent.UpdateAssetInput{
		AgentSpaceId: aws.String(plan.AgentSpaceID.ValueString()),
		AssetId:      aws.String(state.ID.ValueString()),
		Content:      contentPayload,
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

func (r *SkillResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SkillResourceModel

	// Get the plan and state info
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create the input for the SDK
	input := &devopsagent.DeleteAssetInput{
		AgentSpaceId: aws.String(state.AgentSpaceID.ValueString()),
		AssetId:      aws.String(state.ID.ValueString()),
	}

	// Execute via SDK
	_, err := r.client.DeleteAsset(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError("Error deleting skill", err.Error())
		return
	}
}
