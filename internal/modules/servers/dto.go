package servers

type upsertServerRequest struct {
	Name        string            `json:"name" binding:"required,max=128"`
	Description string            `json:"description" binding:"max=1024"`
	Address     string            `json:"address" binding:"max=256"`
	Location    string            `json:"location" binding:"max=256"`
	Tags        []string          `json:"tags"`
	Labels      map[string]string `json:"labels"`
	// AgentToken is optional on create: supply one to pre-provision the
	// agent, or leave empty to have Runix generate it.
	AgentToken string `json:"agentToken" binding:"max=256"`
}

type rotateTokenRequest struct {
	AgentToken string `json:"agentToken" binding:"max=256"`
}

type upsertGroupRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	Description string `json:"description" binding:"max=512"`
}

type groupMemberRequest struct {
	ServerID string `json:"serverId" binding:"required,uuid"`
}
