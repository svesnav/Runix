package rbac

type createRoleRequest struct {
	Key         string   `json:"key" binding:"required,max=64"`
	Name        string   `json:"name" binding:"required,max=128"`
	Description string   `json:"description" binding:"max=512"`
	Permissions []string `json:"permissions"`
}

type updateRoleRequest struct {
	Name        string   `json:"name" binding:"required,max=128"`
	Description string   `json:"description" binding:"max=512"`
	Permissions []string `json:"permissions"`
}

type setUserRolesRequest struct {
	RoleIDs []string `json:"roleIds" binding:"required"`
}

type createGroupRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	Description string `json:"description" binding:"max=512"`
}

type groupMemberRequest struct {
	UserID string `json:"userId" binding:"required,uuid"`
}

type createGrantRequest struct {
	SubjectType SubjectType `json:"subjectType" binding:"required"`
	SubjectID   string      `json:"subjectId" binding:"required,uuid"`
	Permission  string      `json:"permission" binding:"required"`
	ScopeType   ScopeType   `json:"scopeType" binding:"required"`
	ScopeID     string      `json:"scopeId"`
}
