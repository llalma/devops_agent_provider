package resources

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/document"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	tfTypes "github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &InstructionsResource{}
var _ resource.ResourceWithConfigure = &InstructionsResource{}

type InstructionsResource struct {
	client *devopsagent.Client
}

// Define the inputs
type InstructionsResourceModel struct {
	ID           tfTypes.String `tfsdk:"id"`
	AgentType    tfTypes.String `tfsdk:"agent_type"`
	ContentFile  tfTypes.String `tfsdk:"content_file"`
	ContentHash  tfTypes.String `tfsdk:"content_hash"`
	AgentSpaceID tfTypes.String `tfsdk:"agentspace_id"`
}

// Define the resource name
func (r *InstructionsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_instructions"
}

func (r *InstructionsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *InstructionsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
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
			"agent_type": schema.StringAttribute{
				MarkdownDescription: "Which Agent the skill applies to",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.OneOf(
						"GENERIC",
						"CHAT",
						"INCIDENT_TRIAGE",
						"INCIDENT_RCA",
						"INCIDENT_MITIGATION",
						"PREVENTION",
						"RELEASE_READINESS_REVIEW",
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

type InstructionMetadata struct {
	AgentType string `document:"agent_type"`
}

// Create the content payload
func (m *InstructionsResourceModel) ContentPayload() (*types.AssetContentMemberFile, diag.Diagnostics) {
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
			Path: aws.String("AGENTS.md"),
			Body: &types.AssetFileBodyMemberBytes{
				Value: contentBytes,
			},
		},
	}, diags
}

// Create the metadata payload
func (m *InstructionsResourceModel) MetadataPayload(ctx context.Context) document.Interface {
	return document.NewLazyDocument(InstructionMetadata{
		// Name:        m.Name.ValueString(),
		// Description: m.Description.ValueStringPointer(),
		AgentType: m.AgentType.ValueString(),
	})
}

// Runs every plan - Used to generate file hash from path instead of user passing it in
func (r *InstructionsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Skip if resource is being destroyed
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan InstructionsResourceModel
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

func (r *InstructionsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan InstructionsResourceModel
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
		AssetType:    aws.String("AGENTS.md"),
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

func (r *InstructionsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state InstructionsResourceModel

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
	var metadata InstructionMetadata
	err = output.Asset.Metadata.UnmarshalSmithyDocument(&metadata)
	if err != nil {
		resp.Diagnostics.AddError("Metadata Error", err.Error())
		return
	}

	// Extract metadata
	state.AgentType = tfTypes.StringValue(metadata.AgentType)

	file_input := &devopsagent.GetAssetFileInput{
		AgentSpaceId: aws.String(state.AgentSpaceID.ValueString()),
		AssetId:      aws.String(state.ID.ValueString()),
		Path:         aws.String("AGENTS.md"),
	}

	// Fetch the content
	output_file, x := r.client.GetAssetFile(ctx, file_input)
	if x != nil {
		resp.Diagnostics.AddError("File errpr", x.Error())
		return
	}
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
			// contentStr := string(fileBytes)

			// // Some reason if editing via the UI it adds in name and description into the file. Causing the hash to differ. If its present only get the actual file content
			// if strings.HasPrefix(strings.TrimSpace(contentStr), "---") {
			// 	parts := strings.SplitN(contentStr, "---", 3)
			// 	if len(parts) == 3 {
			// 		// Remove residual leading newlines after closing delimiter
			// 		bodyStr := strings.TrimLeft(parts[2], "\r\n")
			// 		fileBytes = []byte(bodyStr)
			// 	}
			// }

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

func (r *InstructionsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state InstructionsResourceModel

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

func (r *InstructionsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state InstructionsResourceModel

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
