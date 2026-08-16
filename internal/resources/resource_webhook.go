package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	devopsTypes "github.com/aws/aws-sdk-go-v2/service/devopsagent/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfTypes "github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/llalma/devops_agent_provider/internal/client"
)

var _ resource.Resource = &WebhookResource{}
var _ resource.ResourceWithConfigure = &WebhookResource{}

type WebhookResource struct {
	config         aws.Config
	devops_client  *devopsagent.Client
	secrets_client *secretsmanager.Client
}

// Define the inputs
type WebhookResourceModel struct {
	ID           tfTypes.String `tfsdk:"id"`
	AgentSpaceID tfTypes.String `tfsdk:"agentspace_id"`
	AuthType     tfTypes.String `tfsdk:"auth_type"`
	SecretARN    tfTypes.String `tfsdk:"secret_arn"`

	WebhookURL types.String `tfsdk:"webhook_url"`
}

// Define the resource name
func (r *WebhookResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *WebhookResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.config = client.Config
	r.secrets_client = client.SecretsClient
	r.devops_client = client.DevopsClient
}

func (r *WebhookResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the devops webhook.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Unique identifier for the skill asset.",
				Computed:            true,

				// Will stop plan from thinking the ID will be replaced
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"agentspace_id": schema.StringAttribute{
				MarkdownDescription: "The agent_space_id of the DevOps agent space.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"auth_type": schema.StringAttribute{
				MarkdownDescription: "The authentication method for the webhook",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.OneOf("apikey"),
				},
			},
			"secret_arn": schema.StringAttribute{
				MarkdownDescription: "ARN of the aws secrets store. to store generated API key",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"webhook_url": schema.StringAttribute{
				MarkdownDescription: "URL of the webhook",
				Computed:            true,
			},
		},
	}
}

// This is an undocumented POST request. I found this from the network tab while creating in the UI
func (r *WebhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan WebhookResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create the request body
	reqBody := map[string]any{
		"serviceId": "event_channel",
		"configuration": map[string]any{
			"eventChannel": map[string]any{
				"webhookAuthType": plan.AuthType.ValueString(),
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		resp.Diagnostics.AddError("Failed to marshal request body", err.Error())
		return
	}

	// Create the URL path
	path := fmt.Sprintf("/v1/agentspaces/%s/associations", plan.AgentSpaceID.ValueString())

	// Send the actual request
	respBytes, err := client.RawPost(ctx, r.config, path, bodyBytes)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create webhook", err.Error())
		return
	}

	// Convert response to josn
	var parsed map[string]any
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		resp.Diagnostics.AddError(
			"Failed to parse response",
			fmt.Sprintf("error: %s\nraw response: %s", err, string(respBytes)),
		)
		return
	}
	association, _ := parsed["association"].(map[string]any)
	webhook, _ := parsed["webhook"].(map[string]any)

	// Store the generated values
	plan.ID = types.StringValue(association["associationId"].(string))
	plan.WebhookURL = types.StringValue(webhook["webhookUrl"].(string))

	// Store the generated API key in given secrets arn
	_, err = r.secrets_client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(plan.SecretARN.ValueString()),
		SecretString: aws.String(webhook["apiKey"].(string)),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to add api-key to secrets manager", err.Error())
		return
	}

	diags = resp.State.Set(ctx, &plan)
	resp.Diagnostics.Append(diags...)
}

func (r *WebhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Get the current state
	var state WebhookResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	//Input for request
	input := &devopsagent.GetAssociationInput{
		AgentSpaceId:  aws.String(state.AgentSpaceID.ValueString()),
		AssociationId: aws.String(state.ID.ValueString()),
	}

	// Fetch the assosciate and check its still present otherwise remove from state
	_, err := r.devops_client.GetAssociation(ctx, input)
	if err != nil {
		// If the resource was deleted outside Terraform, remove it from state
		var notFoundErr *devopsTypes.ResourceNotFoundException
		if errors.As(err, &notFoundErr) {
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError("Failed to read webhook association", err.Error())
		return
	}

	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
}

// Bare minimum for update as there is no way to update without a replacement
func (r *WebhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan WebhookResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save plan to state (satisfies interface if in-place update is ever attempted)
	diags = resp.State.Set(ctx, &plan)
	resp.Diagnostics.Append(diags...)
}

func (r *WebhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Get the current state
	var state WebhookResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Invalidate the latest version of secret. Set to "No Valid API Key" Cant delete a secret without messing with tf state.
	_, err := r.secrets_client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(state.SecretARN.ValueString()),
		SecretString: aws.String("No Valid API Key"),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to update secrets manager", err.Error())
		return
	}

	// Dissasosciate the webhook - Use the sdk this time
	input := &devopsagent.DisassociateServiceInput{
		AgentSpaceId:  aws.String(state.AgentSpaceID.ValueString()),
		AssociationId: aws.String(state.ID.ValueString()),
	}

	// Execute via SDK
	_, err = r.devops_client.DisassociateService(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError("Failed to dissasosciate webhook", err.Error())
		return
	}
}
