# devops_agent_provider

#### Build
```bash
 go build -o /home/llalma/devops_agent_provider/terraform-provider-devops-agent
```

#### Example Usage
```terraform
resource "awscc_devopsagent_agent_space" "example" {
  name        = "example-agent-space3"
  description = "Example DevOps agent space"
}


resource "devops_agent_skill" "example" {
  agentspace_id = awscc_devopsagent_agent_space.example.agent_space_id

  name         = "skill-2"
  description  = "test description4"
  agent_types  = ["CHAT", "INCIDENT_RCA"]
  content_file = "${path.module}/skills/python_reviewer.md"
}

resource "devops_agent_instructions" "example" {
  agentspace_id = awscc_devopsagent_agent_space.example.agent_space_id

  agent_type  = "PREVENTION"
  content_file = "${path.module}/instructions/1.md"
}

# Need to create this to store the secret key generated from the webhook
resource "aws_secretsmanager_secret" "api_key_store" {
  name        = "devops/generated-api-key3"
  description = "Destination for API key created by customprovider"
}

resource "devops_agent_webhook" "example" {
  agentspace_id = awscc_devopsagent_agent_space.example.agent_space_id
  auth_type = "apikey"
  secret_arn = aws_secretsmanager_secret.api_key_store.arn
}
```
